package integration_test

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"open-cluster-management.io/registration/pkg/spoke"
	"open-cluster-management.io/registration/test/integration/util"

	"k8s.io/apimachinery/pkg/api/meta"
)

var _ = ginkgo.Describe("Agent detached mode", func() {

	ginkgo.It("spoke agent runs outside of the managed cluster, detached mode", func() {
		var err error
		managedClusterName := "detached-test-cluster1"

		hubKubeconfigSecret := "detached-test-hub-kubeconfig-secret"
		hubKubeconfigDir := path.Join(util.TestDir, "detached-agent-test", "hub-kubeconfig")
		bootstrapFile := path.Join(util.TestDir, "detached-agent-test", "kubeconfig")

		ginkgo.By("Create bootstrap kubeconfig")
		err = authn.CreateBootstrapKubeConfigWithCertAge(bootstrapFile, serverCertFile, securePort, 20*time.Second)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		ginkgo.By("run registration agent")
		agentOptions := spoke.SpokeAgentOptions{
			ClusterName:              managedClusterName,
			BootstrapKubeconfig:      bootstrapFile,
			HubKubeconfigSecret:      hubKubeconfigSecret,
			HubKubeconfigDir:         hubKubeconfigDir,
			SpokeKubeconfig:          detachedSpokeKubeconfigFile,
			ClusterHealthCheckPeriod: 1 * time.Minute,
			// set the SpokeExternalServerURL with detach cluster URL
			SpokeExternalServerURLs: []string{detachedCfg.Host},
		}

		stopAgent := util.RunAgent("detached-test", agentOptions, spokeCfg)
		defer stopAgent()

		ginkgo.By("Check existence of csr and ManagedCluster")
		// the csr should be created
		gomega.Eventually(func() bool {
			_, err := util.FindUnapprovedSpokeCSR(kubeClient, managedClusterName)
			return err == nil
		}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue())

		// the spoke cluster should be created
		gomega.Eventually(func() bool {
			if _, err := util.GetManagedCluster(clusterClient, managedClusterName); err != nil {
				return false
			}
			return true
		}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue())

		ginkgo.By("Accept ManagedCluster and approve csr")
		err = util.AcceptManagedCluster(clusterClient, managedClusterName)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		err = authn.ApproveSpokeClusterCSR(kubeClient, managedClusterName, time.Second*20)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		ginkgo.By("Check if hub kubeconfig secret is updated")
		// the hub kubeconfig secret should be filled after the csr is approved
		gomega.Eventually(func() bool {
			if _, err := util.GetFilledHubKubeConfigSecret(kubeClient, testNamespace, hubKubeconfigSecret); err != nil {
				return false
			}
			return true
		}, eventuallyTimeout, eventuallyInterval).Should(gomega.BeTrue())

		ginkgo.By("Check if ManagedCluster joins the hub")
		// the spoke cluster should have joined condition finally
		gomega.Eventually(func() error {
			spokeCluster, err := util.GetManagedCluster(clusterClient, managedClusterName)
			if err != nil {
				return err
			}
			if !meta.IsStatusConditionTrue(spokeCluster.Status.Conditions, clusterv1.ManagedClusterConditionJoined) {
				return fmt.Errorf("cluster should be joined")
			}
			return nil
		}, eventuallyTimeout, eventuallyInterval).ShouldNot(gomega.HaveOccurred())

		// check whether the managed cluster is detached cluster by client config CABundle.
		gomega.Eventually(func() error {
			spokeCluster, err := util.GetManagedCluster(clusterClient, managedClusterName)
			if err != nil {
				return err
			}
			for _, config := range spokeCluster.Spec.ManagedClusterClientConfigs {
				if config.URL == detachedCfg.Host && bytes.Equal(config.CABundle, detachedCfg.CAData) {
					return nil
				}
			}
			return errors.New("managed cluster client config does not match")
		}, eventuallyTimeout, eventuallyInterval).ShouldNot(gomega.HaveOccurred())
	})
})
