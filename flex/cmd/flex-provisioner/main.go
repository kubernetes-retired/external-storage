package main

import (
	"flag"
	"strings"

	"github.com/kubernetes-incubator/external-storage/flex/pkg/volume"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	provisioner := flag.String(
		"provisioner",
		"vendor/provisioner",
		"Name of the provisioner. The provisioner will only provision volumes for claims that "+
			"request a StorageClass with a provisioner field set equal to this name.",
	)
	execCommand := flag.String("execCommand", "/opt/storage/flex-provision.sh", "The provisioner executable.")
	// The flex script for flexDriver=<vendor>/<driver> is in
	// /usr/libexec/kubernetes/kubelet-plugins/volume/exec/<vendor>~<driver>/<driver>
	flexDriver := flag.String("flexDriver", "vendor/driver", "The FlexVolume driver.")
	logDebug := flag.Bool("logDebug", false, "Enable debug logging.")
	flag.Parse()

	logger := log.New()
	logger.Formatter = &log.JSONFormatter{}

	if *logDebug {
		log.SetLevel(log.DebugLevel)
	}

	if errs := validateProvisioner(*provisioner, field.NewPath("provisioner")); len(errs) != 0 {
		logger.
			WithField("provisioner", *provisioner).
			WithField("err", errs).
			Fatal("Invalid provisioner")
	}

	if execCommand == nil {
		logger.Error("Must provide provisioner exec command")
		flag.PrintDefaults()
		return
	}

	if flexDriver == nil || *flexDriver == "" {
		logger.Error("Nust provide FlexVolume driver name")
		flag.PrintDefaults()
		return
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		logger.
			WithField("err", err).
			Fatal("Failed to create Kubernetes config")
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.
			WithField("err", err).
			Fatal("Failed to create Kubernetes client")
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := client.Discovery().ServerVersion()
	if err != nil {
		logger.
			WithField("err", err).
			Fatal("Error getting Kubernetes server version")
	}

	// Create the provisioner: it implements the Provisioner interface expected by
	// the controller
	flexProvisioner := volume.NewFlexProvisioner(client, *execCommand, *flexDriver, logger)

	// Start the provision controller which will dynamically provision NFS PVs
	provisionController := controller.NewProvisionController(
		client,
		*provisioner,
		flexProvisioner,
		serverVersion.GitVersion,
	)

	logger.
		WithField("provisioner", *provisioner).
		WithField("flex_driver", *flexDriver).
		Info("Started volume provisioner.")

	provisionController.Run(wait.NeverStop)
}

// validateProvisioner tests if provisioner is a valid qualified name.
// Copied from https://github.com/kubernetes/kubernetes/blob/release-1.4/pkg/apis/storage/validation/validation.go.
func validateProvisioner(provisioner string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if len(provisioner) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, provisioner))
	}
	if len(provisioner) > 0 {
		for _, msg := range validation.IsQualifiedName(strings.ToLower(provisioner)) {
			allErrs = append(allErrs, field.Invalid(fldPath, provisioner, msg))
		}
	}
	return allErrs
}
