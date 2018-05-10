package volume

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/kubernetes-incubator/external-storage/lib/controller"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/exec"
)

const (
	// Option keys
	provisionCmd = "provision"
	deleteCmd    = "delete"

	optionPVorVolumeName = "kubernetes.io/pvOrVolumeName"
	optionCapacity       = "kubernetes.io/storageCapacity"
	// StatusSuccess represents the successful completion of command.
	StatusSuccess = "Success"
	// StatusNotSupported represents that the command is not supported.
	StatusNotSupported = "Not supported"
	// A PV annotation for the identity of the flexProvisioner that provisioned it
	annProvisionerID = "Provisioner_Id"
)

// NewFlexProvisioner creates a new flex provisioner
func NewFlexProvisioner(
	client kubernetes.Interface,
	execCommand string,
	flexDriver string,
	logger *log.Logger,
) *flexProvisioner {
	var identity types.UID
	return &flexProvisioner{
		client:      client,
		execCommand: execCommand,
		flexDriver:  flexDriver,
		identity:    identity,
		runner:      exec.New(),
		logger:      logger,
	}
}

type flexProvisioner struct {
	client      kubernetes.Interface
	execCommand string
	flexDriver  string
	identity    types.UID
	runner      exec.Interface
	logger      *log.Logger
}

// Provision creates a volume i.e. the storage asset and returns a PV object for
// the volume.
func (self *flexProvisioner) Provision(
	options controller.VolumeOptions,
) (persistentVolume *v1.PersistentVolume, err error) {
	logger := self.logger.WithField("volume", options.PVName)
	logger.Info("Provision volume call")

	if err := self.createVolume(options, logger); err != nil {
		return nil, err
	}

	defer logger.
		WithField("volume_data", persistentVolume).
		Debug("Provisioned the volume")

	return &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        options.PVName,
			Labels:      map[string]string{},
			Annotations: map[string]string{annProvisionerID: string(self.identity)},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.
					Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				FlexVolume: &v1.FlexPersistentVolumeSource{
					Driver:   self.flexDriver,
					Options:  map[string]string{},
					ReadOnly: false,
				},
			},
		},
	}, nil
}

func (self *flexProvisioner) createVolume(volumeOptions controller.VolumeOptions, logger *log.Entry) error {
	storage := volumeOptions.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	storageCapacity, ok := storage.AsInt64()
	if !ok {
		logger.
			WithField("storage", storage).
			Error("Invalid storage capacity")
		return errors.New(fmt.Sprintf("Invalid storage capacity %v", storage))
	}
	return self.runCommand(provisionCmd, volumeOptions.Parameters, map[string]string{
		optionPVorVolumeName: volumeOptions.PVName,
		optionCapacity:       strconv.FormatInt(storageCapacity, 10),
	}, logger)
}

// Delete a provisioned volume. Returns an error if the volume is not provisioned by this provisioner.
func (self *flexProvisioner) Delete(volume *v1.PersistentVolume) error {
	logger := self.logger.WithField("volume", volume.Name)
	logger.Info("Delete volume call")
	logger.
		WithField("volume_data", volume).
		Debug("Delete volume.")

	if !self.provisioned(volume) {
		logger.Info("Volume was not provisioned by this provisioner.")
		return &controller.IgnoredError{
			Reason: fmt.Sprintf(
				"this provisioner id %s didn't provision volume %q and so can't delete it; id %s did & can",
				self.identity, volume.Name, volume.Annotations[annProvisionerID],
			),
		}
	}
	defer logger.
		WithField("volume_data", volume).
		Debug("Done deleting the volume")
	return self.runCommand(deleteCmd, volume.Spec.FlexVolume.Options, map[string]string{}, logger)
}

func (self *flexProvisioner) provisioned(volume *v1.PersistentVolume) bool {
	provisionerID, ok := volume.Annotations[annProvisionerID]
	return ok && provisionerID == string(self.identity)
}

// driverResponse represents the return value of the driver callout.
type driverResponse struct {
	// Status of the callout. One of "Success", "Failure" or "Not supported".
	Status string `json:"status"`
	// Reason for success/failure.
	Message string `json:"message,omitempty"`
}

func (self *flexProvisioner) runCommand(
	command string,
	volumeOptions map[string]string,
	extraOptions map[string]string,
	logger *log.Entry,
) error {
	logger = logger.
		WithField("executable", self.execCommand).
		WithField("command", command).
		WithField("options", volumeOptions).
		WithField("extra_options", extraOptions)

	options, err := jsonOptions(volumeOptions, extraOptions)
	if err != nil {
		logger.
			WithField("err", err).
			Error("Failed to marshal options")
		return err
	}

	logger.Debug("Executing command")
	output, err := self.runner.Command(self.execCommand, command, string(options)).CombinedOutput()
	logger = logger.WithField("output", string(output))
	if err != nil {
		logger.
			WithField("err", err).
			Error("Driver call failed")
		return err
	}
	logger.Debug("Executed command")

	response := driverResponse{}
	if err := json.Unmarshal(output, &response); err != nil {
		logger.
			WithField("err", err).
			Error("Failed to unmarshal command output")
		return err
	} else if response.Status == StatusNotSupported {
		logger.
			WithField("err", err).
			Error("Command not supported")
		return errors.New(response.Status)
	} else if response.Status != StatusSuccess {
		logger.
			WithField("err", err).
			Error("Command failed")
		return fmt.Errorf("Command failed with message \"%s\"", response.Message)
	}

	logger.Debug("Done running the command")
	return nil
}

func jsonOptions(volumeOptions map[string]string, extraOptions map[string]string) (string, error) {
	options := map[string]string{}
	for key, value := range extraOptions {
		options[key] = value
	}
	for key, value := range volumeOptions {
		options[key] = value
	}

	jsonOptions, err := json.Marshal(options)
	return string(jsonOptions), err
}
