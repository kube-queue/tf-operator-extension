module github.com/kube-queue/tf-operator-extension

go 1.13

require (
	github.com/kube-queue/kube-queue v0.0.0-20210428030751-fee4afc11eb7 // indirect
	github.com/kubeflow/tf-operator v0.5.3
	golang.org/x/time v0.0.0-20210220033141-f8bda1e9f3ba // indirect
	k8s.io/api v0.20.5 // indirect
	k8s.io/client-go v11.0.0+incompatible // indirect
	k8s.io/sample-controller v0.0.0-00010101000000-000000000000
	k8s.io/utils v0.0.0-20210305010621-2afb4311ab10 // indirect
)

replace k8s.io/sample-controller => k8s.io/sample-controller v0.16.9
