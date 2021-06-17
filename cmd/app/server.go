package app

import (
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/sample-controller/pkg/signals"

	"github.com/kube-queue/kube-queue/pkg/apis/queue/v1alpha2"
	queueversioned "github.com/kube-queue/kube-queue/pkg/client/clientset/versioned"
	queueinformers "github.com/kube-queue/kube-queue/pkg/client/informers/externalversions"
	"github.com/kube-queue/tf-operator-extension/cmd/app/options"
	"github.com/kube-queue/tf-operator-extension/pkg/contorller"
	"github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/tensorflow/v1"
	tfjobversioned "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/client/clientset/versioned"
	tfjobinformers "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/client/informers/externalversions"
	"k8s.io/klog/v2"
)

// Run runs the server.
func Run(opt *options.ServerOption) error {
	klog.Info("1")
	tfExtensionController := contorller.NewTFExtensionController()

	restConfig, err := clientcmd.BuildConfigFromFlags("", opt.KubeConfig)
	if err != nil {
		return err
	}

	queueClient, err := queueversioned.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	queueInformerFactory := queueinformers.NewSharedInformerFactory(queueClient, 0)
	queueInformer := queueInformerFactory.Queue().V1alpha2().QueueUnits().Informer()
	klog.Info("2")
	queueInformer.AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				switch qu := obj.(type) {
				case *v1alpha2.QueueUnit:
					if qu.Spec.ConsumerRef.Kind == "TFJob" && qu.Spec.ConsumerRef.APIVersion == "kubeflow.org/v1" {
						return true
					}
					return false
				default:
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc:    tfExtensionController.AddQueueUnit,
				UpdateFunc: tfExtensionController.UpdateQueueUnit,
				//DeleteFunc: tfExtensionController.DeleteQueueUnit,
			},
		},
	)

	tfjobClient, err := tfjobversioned.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	klog.Info("3")
	tfjobInformerFactory := tfjobinformers.NewSharedInformerFactory(tfjobClient, 0)
	tfjobInformer := tfjobInformerFactory.Kubeflow().V1().TFJobs().Informer()
	tfjobInformer.AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				switch obj.(type) {
				case *v1.TFJob:
					return true
				default:
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				//AddFunc:    tfExtensionController.AddTFJob,
				UpdateFunc: tfExtensionController.UpdateTFJob,
				DeleteFunc: tfExtensionController.DeleteTFJob,
			},
		},
	)

	tfExtensionController.QueueInformer = queueInformer
	tfExtensionController.TfjobInformer = tfjobInformer
	tfExtensionController.QueueClient = queueClient
	tfExtensionController.TfjobClient = tfjobClient
	klog.Info("4")
	// Set up signals so we handle the first shutdown signal gracefully.
	stopCh := signals.SetupSignalHandler()
	err = tfExtensionController.Run(stopCh)
	if err != nil {
		klog.Error(err.Error())
	}

	return nil
}
