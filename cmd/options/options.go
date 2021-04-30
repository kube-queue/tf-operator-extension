package options

import (
	"flag"
	"os"

	v1 "k8s.io/api/core/v1"
)

const (
	TFJobNamespace = "TFJOB_NAMESPACE"
	KubeConfigPath = "KUBECONFIG"
)

// ServerOption is the main context object for the queue controller.
type ServerOption struct {
	Kubeconfig string
	Namespace  string

	ListenTo  string
	QueueAddr string
}

func NewServerOption() *ServerOption {
	s := ServerOption{}
	return &s
}

func (s *ServerOption) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&s.Kubeconfig, "kubeconfig", "", "the path of kube-config")
	fs.StringVar(&s.Namespace, "namespace", v1.NamespaceAll,
		`The namespace to monitor tfjobs. If unset, it monitors all namespaces cluster-wide.
                If set, it only monitors tfjobs in the given namespace.`)

	fs.StringVar(&s.ListenTo, "listen", "unix:///workspace/tf-job-bundle.sock",
		"the address this server will listen to")
	fs.StringVar(&s.QueueAddr, "queue-addr", "unix:///workspace/queue-controller.sock",
		"the address of queue-controller listens to")
}

func ProcessOptions(opt *ServerOption) {
	// Set Namespace
	namespace := os.Getenv(TFJobNamespace)
	if len(namespace) > 0 {
		opt.Namespace = namespace
	}

	if len(os.Getenv(KubeConfigPath)) > 0 {
		opt.Kubeconfig = os.Getenv(KubeConfigPath)
	}
}