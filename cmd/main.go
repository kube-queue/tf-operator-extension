package main

import (
	"flag"

	"github.com/kube-queue/tf-operator-extension/cmd/options"
	"github.com/kube-queue/tf-operator-extension/pkg/informer"
	"github.com/kube-queue/tf-operator-extension/pkg/server"
	tfjobClient "github.com/kubeflow/tf-operator/pkg/client/clientset/versioned"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"k8s.io/sample-controller/pkg/signals"
)

func main() {
	opt := options.NewServerOption()
	opt.AddFlags(flag.CommandLine)

	flag.Parse()
	options.ProcessOptions(opt)

	stopCh := signals.SetupSignalHandler()

	cfg, err := clientcmd.BuildConfigFromFlags("", opt.Kubeconfig)
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %s\n", err.Error())
	}

	i, err := informer.MakeInformer(cfg, opt.QueueAddr, opt.Namespace)
	if err != nil {
		klog.Fatalf("Error making informer for tfjob: %s\n", err.Error())
	}

	i.Run(stopCh)

	tfci, err := tfjobClient.NewForConfig(cfg)
	if err != nil {
		klog.Fatalln(err)
	}
	extensionServer := server.MakeExtensionServer(tfci)

	if err = server.StartServer(extensionServer, opt.ListenTo); err != nil {
		klog.Fatalln(err)
	}
}
