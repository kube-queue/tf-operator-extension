package app

import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/sample-controller/pkg/signals"

	"github.com/kube-queue/api/pkg/apis/scheduling/v1alpha1"
	queueversioned "github.com/kube-queue/api/pkg/client/clientset/versioned"
	queueinformers "github.com/kube-queue/api/pkg/client/informers/externalversions"
	"github.com/kube-queue/tf-operator-extension/cmd/app/options"
	"github.com/kube-queue/tf-operator-extension/pkg/contorller"
	tfjobv1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/apis/tensorflow/v1"
	tfjobversioned "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/client/clientset/versioned"
	tfjobinformers "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/client/informers/externalversions"
	"k8s.io/klog/v2"
)

// Run runs the server.
func Run(opt *options.ServerOption) error {
	var restConfig *rest.Config
	var err error
	// Set up signals so we handle the first shutdown signal gracefully.
	stopCh := signals.SetupSignalHandler()

	if restConfig, err = rest.InClusterConfig(); err != nil {
		if restConfig, err = clientcmd.BuildConfigFromFlags("", opt.KubeConfig); err != nil {
			return err
		}
	}

	queueClient, err := queueversioned.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	tfJobClient, err := tfjobversioned.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	queueInformerFactory := queueinformers.NewSharedInformerFactory(queueClient, 0)
	queueInformer := queueInformerFactory.Scheduling().V1alpha1().QueueUnits().Informer()
	tfJobInformerFactory := tfjobinformers.NewSharedInformerFactory(tfJobClient, 0)
	tfJobInformer := tfJobInformerFactory.Kubeflow().V1().TFJobs().Informer()

	tfExtensionController := contorller.NewTFExtensionController(queueInformerFactory.Scheduling().V1alpha1().QueueUnits(),
		queueClient,
		tfJobInformerFactory.Kubeflow().V1().TFJobs(),
		tfJobClient)

	queueInformer.AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				switch qu := obj.(type) {
				case *v1alpha1.QueueUnit:
					if qu.Spec.ConsumerRef != nil &&
						qu.Spec.ConsumerRef.Kind == contorller.ConsumerRefKind &&
						qu.Spec.ConsumerRef.APIVersion == contorller.ConsumerRefAPIVersion {
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
				DeleteFunc: tfExtensionController.DeleteQueueUnit,
			},
		},
	)

	tfJobInformer.AddEventHandler(
		cache.FilteringResourceEventHandler{
			FilterFunc: func(obj interface{}) bool {
				switch obj.(type) {
				case *tfjobv1.TFJob:
					return true
				default:
					return false
				}
			},
			Handler: cache.ResourceEventHandlerFuncs{
				AddFunc:    tfExtensionController.AddTFJob,
				UpdateFunc: tfExtensionController.UpdateTFJob,
				DeleteFunc: tfExtensionController.DeleteTFJob,
			},
		},
	)

	// start queueunit informer
	go queueInformerFactory.Start(stopCh)
	// start pytorchjob informer
	go tfJobInformerFactory.Start(stopCh)

	err = tfExtensionController.Run(2, stopCh)
	if err != nil {
		klog.Fatalf("Error running pytorchExtensionController", err.Error())
		return err
	}

	return nil
}
