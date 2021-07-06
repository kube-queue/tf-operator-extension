package contorller

import (
	"context"
	"fmt"
	"time"

	queueinformers "github.com/kube-queue/api/pkg/client/informers/externalversions/scheduling/v1alpha1"
	tfjobinformers "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/client/informers/externalversions/tensorflow/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	"github.com/kube-queue/api/pkg/apis/scheduling/v1alpha1"
	queueversioned "github.com/kube-queue/api/pkg/client/clientset/versioned"
	commonv1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/common/v1"
	v1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/tensorflow/v1"
	tfjobversioned "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	// MaxRetries is the number of times a queue item will be retried before it is dropped out of the queue.
	// With the current rate-limiter in use (5ms*2^(maxRetries-1)) the following numbers represent the times
	// a queue item is going to be requeued:
	//
	// 1-10 retry times: 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s,
	// 11-20 retry times: 5.1s, 10.2s, 20.4s, 41s, 82s, 164s, 328s, 656s(11min), 1312s(21min), 2624s(43min)
	MaxRetries = 15
	// Suspend is a flag annotation for tfjob to use the queueunit crd
	Suspend = "scheduling.x-k8s.io/suspend"
)

type TFExtensionController struct {
	queueInformer queueinformers.QueueUnitInformer
	queueClient   *queueversioned.Clientset
	tfjobInformer tfjobinformers.TFJobInformer
	tfjobClient   *tfjobversioned.Clientset
	workqueue     workqueue.RateLimitingInterface
}

func NewTFExtensionController(queueInformer queueinformers.QueueUnitInformer,
	queueClient *queueversioned.Clientset,
	tfJobInformer tfjobinformers.TFJobInformer,
	tfJobClinet *tfjobversioned.Clientset) *TFExtensionController {
	return &TFExtensionController{
		queueInformer: queueInformer,
		queueClient:   queueClient,
		tfjobInformer: tfJobInformer,
		tfjobClient:   tfJobClinet,
		workqueue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "QueueUnit"),
	}
}

func (tc *TFExtensionController) Run(threadiness int, stopCh <-chan struct{}) error {
	defer runtime.HandleCrash()
	defer tc.workqueue.ShutDown()

	klog.Info("Start TFExtensionController Run function")
	if !cache.WaitForCacheSync(stopCh, tc.queueInformer.Informer().HasSynced) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(tc.runWorker, time.Second, stopCh)
	}
	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

func (tc *TFExtensionController) runWorker() {
	for tc.processNextWorkItem() {
	}
}

func (tc *TFExtensionController) processNextWorkItem() bool {
	obj, shutdown := tc.workqueue.Get()
	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer t.workqueue.Done.
	err := func(obj interface{}) error {
		defer tc.workqueue.Done(obj)
		var key string
		var ok bool

		if key, ok = obj.(string); !ok {
			tc.workqueue.Forget(obj)
			runtime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}

		err := tc.syncHandler(key)
		tc.handleErr(err, key)

		return nil
	}(obj)

	if err != nil {
		runtime.HandleError(err)
	}

	return true
}

func (tc *TFExtensionController) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return err
	}
	// Get queueunit from cache
	queueUnit, err := tc.queueInformer.Lister().QueueUnits(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			klog.Errorf("QueueUnit %v/%v has been deleted from cache", queueUnit.Namespace, queueUnit.Name)
			// If can't get queueunit, return nil, handleErr function will forget key from workqueue
			return nil
		}
		runtime.HandleError(fmt.Errorf("failed to get queueunit by: %s/%s", namespace, name))

		return err
	}
	klog.Infof("Get informer from add/update event,queueUnit:%v/%v", queueUnit.Namespace, queueUnit.Name)

	if queueUnit.Status.Phase == v1alpha1.Dequeued {
		klog.Infof("QueueUnit %v/%v has dequeued", queueUnit.Namespace, queueUnit.Name)
		err = tc.deleteQueueAnotationInTFJob(queueUnit)
		if errors.IsNotFound(err) {
			// If can't find tfjob for queueunit, return err, handleErr function will requeue key MaxRetries times
			return err
		}
	}

	return nil
}

func (tc *TFExtensionController) deleteQueueUnitWhenJobNotFound(key string) {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return
	}

	err = tc.deleteQueueUnitInstance(namespace, name)
	if err != nil {
		klog.Errorf("Delete queueunit error: %v/%v %v", namespace, name, err.Error())
		return
	}
	klog.Warningf("Delete queueunit %v/%v because can't find related tfjob ", namespace, name)
}

func (tc *TFExtensionController) handleErr(err error, key string) {
	if err == nil {
		tc.workqueue.Forget(key)
		return
	}
    // numRequeues defined how many times the item was requeued
	numRequeues := tc.workqueue.NumRequeues(key)
	if numRequeues < MaxRetries {
		tc.workqueue.AddRateLimited(key)
		klog.Infof("We will requeue %v %d times,because:%v, has retried %d times", key, MaxRetries, err, numRequeues + 1)
		return
	}

	runtime.HandleError(err)
	klog.Infof("Dropping queueunit %q out of the workqueue: %v", key, err)
	tc.workqueue.Forget(key)
	// If still can't find job after retry, delete queueunit
	tc.deleteQueueUnitWhenJobNotFound(key)
}

func (tc *TFExtensionController) enqueueQueueUnit(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		runtime.HandleError(err)
		return
	}
	tc.workqueue.AddRateLimited(key)
}

func (tc *TFExtensionController) AddQueueUnit(obj interface{}) {
	qu := obj.(*v1alpha1.QueueUnit)
	klog.Infof("Add queueunit:%v/%v", qu.Namespace, qu.Name)
	tc.enqueueQueueUnit(qu)
}

func (tc *TFExtensionController) UpdateQueueUnit(oldObj, newObj interface{}) {
	oldQu := oldObj.(*v1alpha1.QueueUnit)
	newQu := newObj.(*v1alpha1.QueueUnit)
	if oldQu.ResourceVersion == newQu.ResourceVersion {
		return
	}
	tc.enqueueQueueUnit(newQu)
}

func (tc *TFExtensionController) DeleteQueueUnit(obj interface{}) {
	qu := obj.(*v1alpha1.QueueUnit)
	klog.Infof("QueueUnit deleted:%v/%v", qu.Namespace, qu.Name)
}

func (tc *TFExtensionController) AddTFJob(obj interface{}) {
	tfJob := obj.(*v1.TFJob)
	klog.Infof("Add tfjob:%v/%v", tfJob.Namespace, tfJob.Name)
}

func (tc *TFExtensionController) UpdateTFJob(_, newObj interface{}) {
	newJob := newObj.(*v1.TFJob)
	conditionsLen := len(newJob.Status.Conditions)
	if conditionsLen > 0 {
		lastCondition := newJob.Status.Conditions[conditionsLen-1]
		if lastCondition.Type == commonv1.JobFailed || lastCondition.Type == commonv1.JobSucceeded {
			klog.Infof("job %v/%v finished, current lastCondition.Type: [%v]", newJob.Namespace, newJob.Name, lastCondition.Type)
			tc.deleteQueueUnitAfterJobTerminated(newJob)
		}
	}
}

func (tc *TFExtensionController) DeleteTFJob(obj interface{}) {
	job := obj.(*v1.TFJob)
	tc.deleteQueueUnitAfterJobTerminated(job)
}

func (tc *TFExtensionController) deleteQueueUnitAfterJobTerminated(job *v1.TFJob) {
	qulist, err := tc.queueClient.SchedulingV1alpha1().QueueUnits(job.Namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		klog.Errorf("DeleteTFJob error: get qulist failed %v/%v %v", job.Namespace, job.Name, err.Error())
		return
	}

	for _, qu := range qulist.Items {
		if qu.Spec.ConsumerRef.Name == job.Name {
			err = tc.deleteQueueUnitInstance(job.Namespace, qu.Name)
			if err != nil {
				klog.Errorf("Delete queueunit error: delete qu failed %v/%v %v", qu.Namespace, qu.Name, err)
			}
			klog.Infof("Delete queueunit %s because related tfjob %v/%v terminated", qu.Name, job.Namespace, job.Name)
		}
	}
}

// TODO: How to deal with the failure to delete the queueunit instance
func (tc *TFExtensionController) deleteQueueUnitInstance(namespace, name string) error {
	err := tc.queueClient.SchedulingV1alpha1().QueueUnits(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (tc *TFExtensionController) deleteQueueAnotationInTFJob(qu *v1alpha1.QueueUnit) error {
	namespace := qu.Spec.ConsumerRef.Namespace
	tfJobName := qu.Spec.ConsumerRef.Name
	tfJob, err := tc.tfjobClient.KubeflowV1().TFJobs(qu.Spec.ConsumerRef.Namespace).Get(context.TODO(), tfJobName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			klog.Warningf("Can not find related tfjob:%v for queueunit:%v in namespace:%v", tfJobName, qu.Name, namespace)
			return err
		}
		klog.Errorf("Get tfjob failed %v/%v %v", namespace, tfJobName, err.Error())
		return err
	}

	var annotation = map[string]string{}
	for k, v := range tfJob.Annotations {
		if k != Suspend {
			annotation[k] = v
		}
	}
	tfJob.SetAnnotations(annotation)

	// TODO change to patch
	_, err = tc.tfjobClient.KubeflowV1().TFJobs(namespace).Update(context.TODO(), tfJob, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf("UpdateQueueUnit error: update tfjob failed %v/%v %v", namespace, tfJobName, err.Error())
		return err
	}
	klog.Infof("Update annotations for tfjob %v/%v", tfJob.Namespace, tfJob.Name)

	return nil
}
