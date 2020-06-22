module github.com/open-cluster-management/registration

go 1.13

require (
	github.com/containerd/containerd v1.3.4 // indirect
	github.com/containerd/continuity v0.0.0-20200413184840-d3ef23f19fbb // indirect
	github.com/containers/storage v1.20.2 // indirect
	github.com/fsouza/go-dockerclient v1.6.5 // indirect
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/klauspost/compress v1.10.9 // indirect
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	github.com/open-cluster-management/api v0.0.0-20200602195039-a516cac2e038
	github.com/openshift/api v0.0.0-20200619200343-6cafd5cd116b
	github.com/openshift/build-machinery-go v0.0.0-20200424080330-082bf86082cc
	github.com/openshift/client-go v0.0.0-20200608144219-584632b8fc73
	github.com/openshift/generic-admission-server v1.14.1-0.20200514123932-ccc9079d8bdb
	github.com/openshift/imagebuilder v1.1.5 // indirect
	github.com/openshift/library-go v0.0.0-20200401114229-ffab8c6e83a9
	github.com/pquerna/ffjson v0.0.0-20190930134022-aa0246cd15f7 // indirect
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.5
	golang.org/x/net v0.0.0-20200602114024-627f9648deb9 // indirect
	golang.org/x/sync v0.0.0-20200317015054-43a5402ce75a // indirect
	golang.org/x/sys v0.0.0-20200622182413-4b0db7f3f76b // indirect
	google.golang.org/genproto v0.0.0-20200622133129-d0ee0c36e670 // indirect
	google.golang.org/grpc v1.30.0 // indirect
	k8s.io/api v0.18.3
	k8s.io/apimachinery v0.18.3
	k8s.io/apiserver v0.18.2
	k8s.io/client-go v0.18.3
	k8s.io/component-base v0.18.2
	k8s.io/klog v1.0.0
	k8s.io/kube-aggregator v0.18.0
	k8s.io/utils v0.0.0-20200327001022-6496210b90e8
	sigs.k8s.io/controller-runtime v0.6.0
)

replace github.com/openshift/api => github.com/deads2k/api v0.0.0-20200624190755-4d69463c94a5

replace github.com/openshift/client-go => github.com/deads2k/client-go-1 v0.0.0-20200622153507-d55c15fb77ea
