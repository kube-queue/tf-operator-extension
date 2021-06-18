package contorller

import (
	"context"

	queuev1alpha2 "github.com/kube-queue/kube-queue/pkg/apis/queue/v1alpha2"
	queueversioned "github.com/kube-queue/kube-queue/pkg/client/clientset/versioned"
	"github.com/kube-queue/kube-queue/pkg/queue"
	commonv1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/common/v1"
	tfjobv1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/tensorflow/v1"
	tfjobversioned "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type TFExtensionController struct {
	TfjobInformer cache.SharedIndexInformer
	QueueInformer cache.SharedIndexInformer
	QueueClient   *queueversioned.Clientset
	TfjobClient   *tfjobversioned.Clientset
}

func NewTFExtensionController() *TFExtensionController {
	return &TFExtensionController{}
}

func (t *TFExtensionController) Run(stopCh <-chan struct{}) error {
	klog.Info("5")
	go t.QueueInformer.Run(stopCh)
	if !cache.WaitForCacheSync(stopCh, t.QueueInformer.HasSynced) {
		klog.Error("timed out waiting for caches to sync queueunit")
		return nil
	}
	klog.Info("6")
	t.TfjobInformer.Run(stopCh)

	return nil
}

func (t *TFExtensionController) AddQueueUnit(obj interface{}) {
	unit := obj.(*queuev1alpha2.QueueUnit)
	klog.Infof("unit add %v", unit.Name)

	if unit.Status.Phase == queuev1alpha2.Dequeued {
		t.DeleteQueueAnotation(unit)
	}
}

func (t *TFExtensionController) DeleteQueueUnit(obj interface{}) {
}

func (t *TFExtensionController) UpdateQueueUnit(oldObj, newObj interface{}) {
	oldQu := oldObj.(*queuev1alpha2.QueueUnit)
	newQu := newObj.(*queuev1alpha2.QueueUnit)

	// TODO add op to workqueue and asynchronous operation
	if oldQu.Status.Phase != queuev1alpha2.Dequeued && newQu.Status.Phase == queuev1alpha2.Dequeued {
		t.DeleteQueueAnotation(newQu)
	}
}

func (t *TFExtensionController) AddTFJob(obj interface{}) {
	//unit := obj.(*v1alpha2.QueueUnit)
}

func (t *TFExtensionController) DeleteTFJob(obj interface{}) {
	job := obj.(*tfjobv1.TFJob)

	//selector := labels.Set(labels.Set{"jobname": job.Name}).String()
	//opts := metav1.ListOptions{
	//	LabelSelector: selector,
	//}

	qulist, err := t.QueueClient.QueueV1alpha2().QueueUnits(job.Namespace).List(context.TODO(), metav1.ListOptions{})
	klog.Infof("qulist %v", qulist)
	if err != nil {
		klog.Errorf("DeleteTFJob error: get qulist failed %v/%v %v", job.Namespace, job.Name, err.Error())
		return
	}
	for _, qu := range qulist.Items {
		//klog.Infof("%v, %v, %v, %v, %v，%v, %v", qu.Spec.ConsumerRef.Kind, job.Kind, qu.Spec.ConsumerRef.APIVersion, job.APIVersion, qu.Spec.ConsumerRef.Name, job.Name)

		if qu.Spec.ConsumerRef.Name == job.Name {
			err = t.QueueClient.QueueV1alpha2().QueueUnits(job.Namespace).Delete(context.TODO(), qu.Name, metav1.DeleteOptions{})
			if err != nil {
				klog.Errorf("DeleteTFJob error: delete qu failed %v/%v %v", qu.Namespace, qu.Name, err)
			}
		}
	}
}

func (t *TFExtensionController) UpdateTFJob(oldObj, newObj interface{}) {
	//oldJob := oldObj.(*tfjobv1.TFJob)
	newJob := newObj.(*tfjobv1.TFJob)

	len := len(newJob.Status.Conditions)
	if len > 0 {
		lastCondition := newJob.Status.Conditions[len-1]
		if lastCondition.Type == commonv1.JobFailed || lastCondition.Type == commonv1.JobSucceeded {
			klog.Infof("job %v/%v if finished[%v]", newJob.Namespace, newJob.Name, lastCondition.Type)
			t.DeleteTFJob(newObj)
		}
	}
}

func (t *TFExtensionController) DeleteQueueAnotation(qu *queuev1alpha2.QueueUnit) {
	namespace := qu.Spec.ConsumerRef.Namespace
	tfjobName := qu.Spec.ConsumerRef.Name
	tfjob, err := t.TfjobClient.KubeflowV1().TFJobs(qu.Spec.ConsumerRef.Namespace).Get(context.TODO(), tfjobName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("UpdateQueueUnit error: get tfjob failed %v/%v %v", namespace, tfjobName, err.Error())
		return
	}

	var annotation = map[string]string{}
	for k, v := range tfjob.Annotations {
		if k != queue.AnnotationQueue {
			annotation[k] = v
		}
	}
	tfjob.SetAnnotations(annotation)

	// TODO change to patch
	_, err = t.TfjobClient.KubeflowV1().TFJobs(namespace).Update(context.TODO(), tfjob, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf("UpdateQueueUnit error: update tfjob failed %v/%v %v", namespace, tfjobName, err.Error())
	}
}
