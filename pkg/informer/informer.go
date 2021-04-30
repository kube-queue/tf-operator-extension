package informer

import (
	queue "github.com/kube-queue/kube-queue/pkg/comm/queue"
	tfjobInformerv1 "github.com/kubeflow/tf-operator/pkg/client/informers/externalversions/tensorflow/v1"
	tfcontroller "github.com/kubeflow/tf-operator/pkg/controller.v1/tensorflow"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"time"
)

var (
	retryPeriod   = 3 * time.Second
)

type Informer struct {
	client   queue.QueueClientInterface
	informer tfjobInformerv1.TFJobInformer
}

func MakeInformer(cfg *rest.Config, addr string, namespace string) (*Informer, error) {
	client, err := MakeQueueClient(addr)
	if err != nil {
		return nil, err
	}

	in := tfcontroller.NewUnstructuredTFJobInformer(cfg, namespace, retryPeriod)
	in.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    client.AddFunc,
		DeleteFunc: client.DeleteFunc,
	})

	return &Informer{
		client:   client,
		informer: in,
	}, nil
}

func (d *Informer) Run(stopCh <-chan struct{}) {
	go d.informer.Informer().Run(stopCh)
}
