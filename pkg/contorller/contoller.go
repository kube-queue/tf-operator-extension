package contorller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"

	queueinformers "github.com/kube-queue/api/pkg/client/informers/externalversions/scheduling/v1alpha1"
	tfjobinformers "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/client/informers/externalversions/tensorflow/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"

	"github.com/kube-queue/api/pkg/apis/scheduling/v1alpha1"
	queueversioned "github.com/kube-queue/api/pkg/client/clientset/versioned"
	commonv1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/common/v1"
	tfjobv1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/tensorflow/v1"
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
	Queuing = "Queuing"
)

const (
	ConsumerRefKind       = tfjobv1.Kind
	ConsumerRefAPIVersion = tfjobv1.GroupName + "/" + tfjobv1.GroupVersion
	// QuNameSuffix is the suffix of the queue unit name when create a new one.
	// In this way, different types of jobs with the same name will create different queue unit name.
	QuNameSuffix = "-tf-qu"
)

type TFExtensionController struct {
	k8sClient     *kubernetes.Clientset
	queueInformer queueinformers.QueueUnitInformer
	queueClient   *queueversioned.Clientset
	tfjobInformer tfjobinformers.TFJobInformer
	tfjobClient   *tfjobversioned.Clientset
	workqueue     workqueue.RateLimitingInterface
}

func NewTFExtensionController(
	k8sClient *kubernetes.Clientset,
	queueInformer queueinformers.QueueUnitInformer,
	queueClient *queueversioned.Clientset,
	tfJobInformer tfjobinformers.TFJobInformer,
	tfJobClinet *tfjobversioned.Clientset) *TFExtensionController {
	return &TFExtensionController{
		k8sClient:     k8sClient,
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
		err = tc.deleteQueueAnnotationInTFJob(queueUnit)
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
		klog.Infof("We will requeue %v %d times,because:%v, has retried %d times", key, MaxRetries, err, numRequeues+1)
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
	tfJob := obj.(*tfjobv1.TFJob)
	klog.Infof("Add tfjob:%v/%v", tfJob.Namespace, tfJob.Name)
	err := tc.createQueueUnitInstance(tfJob)
	if err != nil {
		klog.Errorf("Can't create queueunit for tfjob %v/%v,err is:%v", tfJob.Namespace, tfJob.Name, err)
	}

	tfJob, err = tc.tfjobClient.KubeflowV1().TFJobs(tfJob.Namespace).Get(context.TODO(), tfJob.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			klog.Warningf("Can not find related tfjob:%v in namespace:%v", tfJob.Name, tfJob.Namespace)
		}
		klog.Errorf("Get tfjob failed %v/%v %v", tfJob.Namespace, tfJob.Name, err.Error())
		return
	}

	if tfJob.Status.Conditions == nil {
		tfJob.Status.Conditions = make([]commonv1.JobCondition, 0)
		tfJob.Status.Conditions = append(tfJob.Status.Conditions, commonv1.JobCondition{
			Type:           Queuing,
			LastUpdateTime: metav1.Now(),
		})
		_, err = tc.tfjobClient.KubeflowV1().TFJobs(tfJob.Namespace).UpdateStatus(context.TODO(), tfJob, metav1.UpdateOptions{})
		if err != nil {
			klog.Errorf("update tfjob failed Queuing %v/%v %v", tfJob.Namespace, tfJob.Name, err.Error())
		}
		klog.Infof("update tfjob %v/%v status Queuing successfully", tfJob.Namespace, tfJob.Name)
	}
}

func (tc *TFExtensionController) createQueueUnitInstance(tfJob *tfjobv1.TFJob) error {
	// 1. try to get annotation scheduling.x-k8s.io/suspend
	_, ok := tfJob.Annotations[Suspend]
	if !ok {
		klog.Infof("tfjob %v/%v is not scheduled by kube-queue", tfJob.Namespace, tfJob.Name)
		return nil
	}

	// 2. annotation has been found and try to get queueunit from cache
	qu, err := tc.queueInformer.Lister().QueueUnits(tfJob.Namespace).Get(tfJob.Name + QuNameSuffix)
	if err != nil {
		if errors.IsNotFound(err) {
			// 2.1 there is no specified queueunit in k8s
			klog.Infof("Creating queueunit for tfJob %v/%v", tfJob.Namespace, tfJob.Name)
			// 2.2 generate a new queueunit
			quMeta := tc.generateQueueUnitInstance(tfJob)
			// 2.3 create the queueunit
			qu, err = tc.queueClient.SchedulingV1alpha1().QueueUnits(quMeta.Namespace).Create(context.TODO(), quMeta, metav1.CreateOptions{})
			if err != nil {
				return err
			}
			klog.Infof("Created queueunit %v/%v successfully", qu.Namespace, qu.Name)
			return nil
		} else {
			return err
		}
	}

	if qu.Spec.ConsumerRef.Kind == ConsumerRefKind {
		klog.Infof("It already has a queueunit %v/%v for tfJob %v/%v",
			qu.Namespace, qu.Name, tfJob.Namespace, tfJob.Name)
	} else {
		klog.Warningf("There is an exception queueunit:%v/%v for tfjob in k8s, please check it", qu.Namespace, qu.Name)
	}

	return nil
}

func (tc *TFExtensionController) generateQueueUnitInstance(tfJob *tfjobv1.TFJob) *v1alpha1.QueueUnit {
	// 1. build ObjectReference from corresponding Job CR
	objectReference := tc.generateObjectReference(tfJob)
	// 2. get priorityClassName and priority from one of tfjob roles
	var priorityClassName string
	var priority *int32
	for role := range tfJob.Spec.TFReplicaSpecs {
		priorityClassName = tfJob.Spec.TFReplicaSpecs[role].Template.Spec.PriorityClassName
		priority = tfJob.Spec.TFReplicaSpecs[role].Template.Spec.Priority
		// By default, we think that the PriorityClassName and priority of all roles are the same,
		// so just take the value of one role and break
		break
	}

	// If there is a related priorityClassInstance in K8s, we use priorityClass's value instead of tfJob.Spec.TFReplicaSpecs[role].Template.Spec.Priority
	if priorityClassName != "" {
		priorityClassInstance, err := tc.k8sClient.SchedulingV1().PriorityClasses().Get(context.TODO(), priorityClassName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				klog.Infof("Can not find priority class name %v in k8s, we will ignore it", priorityClassName)
			} else {
				klog.Errorf("Can not get PriorityClass %v from k8s for tfjob:%v/%v, err:%v", priorityClassName, tfJob.Namespace, tfJob.Name, err)
			}
		} else {
			priority = &priorityClassInstance.Value
		}
	}

	// 3. calculate the total resources of this tensorflow instance
	resources := tc.calculateTotalResources(tfJob)
	// 4. build QueueUnit
	return &v1alpha1.QueueUnit{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tfJob.Name + QuNameSuffix,
			Namespace: tfJob.Namespace,
		},
		Spec: v1alpha1.QueueUnitSpec{
			ConsumerRef:       objectReference,
			Priority:          priority,
			PriorityClassName: priorityClassName,
			Resource:          resources,
		},
		Status: v1alpha1.QueueUnitStatus{
			Phase:   v1alpha1.Enqueued,
			Message: "the queueunit is enqueued after created",
		},
	}
}

func (tc *TFExtensionController) generateObjectReference(tfJob *tfjobv1.TFJob) *corev1.ObjectReference {
	return &corev1.ObjectReference{
		APIVersion: ConsumerRefAPIVersion,
		Kind:       ConsumerRefKind,
		Namespace:  tfJob.Namespace,
		Name:       tfJob.Name,
	}
}

func (tc *TFExtensionController) calculateTotalResources(tfJob *tfjobv1.TFJob) corev1.ResourceList {
	totalResources := corev1.ResourceList{}
	// calculate the total resource request
	for _, replicaSpec := range tfJob.Spec.TFReplicaSpecs {
		// get different roles and calculate the sum of the pods belongs to the same role
		count := int(*replicaSpec.Replicas)
		containers := replicaSpec.Template.Spec.Containers
		for _, container := range containers {
			// calculate the resource request of pods first (the pod count is decided by replicas's number)
			resources := container.Resources.Requests
			for resourceType := range resources {
				quantity := resources[resourceType]
				// scale the quantity by count
				replicaQuantity := resource.Quantity{}
				for i := 1; i <= count; i++ {
					replicaQuantity.Add(quantity)
				}
				// check if the resourceType is in totalResources
				if totalQuantity, ok := totalResources[resourceType]; !ok {
					// not in: set this replicaQuantity
					totalResources[resourceType] = replicaQuantity
				} else {
					// in: append this replicaQuantity and update
					totalQuantity.Add(replicaQuantity)
					totalResources[resourceType] = totalQuantity
				}
			}
		}
	}
	return totalResources
}

func (tc *TFExtensionController) UpdateTFJob(_, newObj interface{}) {
	newJob := newObj.(*tfjobv1.TFJob)
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
	job := obj.(*tfjobv1.TFJob)
	tc.deleteQueueUnitAfterJobTerminated(job)
}

func (tc *TFExtensionController) deleteQueueUnitAfterJobTerminated(job *tfjobv1.TFJob) {
	qulist, err := tc.queueClient.SchedulingV1alpha1().QueueUnits(job.Namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		klog.Errorf("DeleteTFJob error: get qulist failed %v/%v %v", job.Namespace, job.Name, err.Error())
		return
	}

	for _, qu := range qulist.Items {
		if qu.Spec.ConsumerRef.Name == job.Name && qu.Spec.ConsumerRef.Kind == ConsumerRefKind {
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

func (tc *TFExtensionController) deleteQueueAnnotationInTFJob(qu *v1alpha1.QueueUnit) error {
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
