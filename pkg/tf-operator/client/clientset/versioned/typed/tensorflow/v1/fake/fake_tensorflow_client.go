// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1 "github.com/kube-queue/tf-operator-extension/pkg/tf-operator/client/clientset/versioned/typed/tensorflow/v1"
	rest "k8s.io/client-go/rest"
	testing "k8s.io/client-go/testing"
)

type FakeKubeflowV1 struct {
	*testing.Fake
}

func (c *FakeKubeflowV1) TFJobs(namespace string) v1.TFJobInterface {
	return &FakeTFJobs{c, namespace}
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *FakeKubeflowV1) RESTClient() rest.Interface {
	var ret *rest.RESTClient
	return ret
}
