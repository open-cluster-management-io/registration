package webhook

import (
	"os"

	criticalresourceadmissionwebhook "github.com/open-cluster-management/registration/pkg/criticalresource-admissionwebhook"

	admissionserver "github.com/openshift/generic-admission-server/pkg/cmd/server"
	"github.com/spf13/cobra"
	genericapiserver "k8s.io/apiserver/pkg/server"
)

func NewAdmissionHook() *cobra.Command {
	o := admissionserver.NewAdmissionServerOptions(os.Stdout, os.Stderr, &criticalresourceadmissionwebhook.CriticalResourceAdmissionWebhook{})

	cmd := &cobra.Command{
		Use:   "admissionwebhook",
		Short: "Start CriticalResource Admission Server",
		RunE: func(c *cobra.Command, args []string) error {
			stopCh := genericapiserver.SetupSignalHandler()

			if err := o.Complete(); err != nil {
				return err
			}
			if err := o.Validate(args); err != nil {
				return err
			}
			if err := o.RunAdmissionServer(stopCh); err != nil {
				return err
			}
			return nil
		},
	}

	o.RecommendedOptions.AddFlags(cmd.Flags())

	return cmd
}
