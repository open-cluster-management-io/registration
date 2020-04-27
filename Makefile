
all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/deps.mk \
	targets/openshift/images.mk \
	targets/openshift/bindata.mk \
)

$(call add-bindata,spokecluster,./pkg/hub/spokecluster/manifests/...,bindata,bindata,./pkg/hub/spokecluster/bindata/bindata.go)

clean:
	$(RM) ./registration
.PHONY: clean

GO_TEST_PACKAGES :=./pkg/... ./cmd/...

test-install-ginkgo:
	go get github.com/onsi/ginkgo/ginkgo
.PHONY: test-install-ginkgo

test-integration: test-install-ginkgo
	ginkgo -v -tags integration --slowSpecThreshold=15 --failFast ./test/integration/...
.PHONY: test-integration
