package container

// ----------------------------------------------------------------------------
// DO NOT EDIT THIS FILE
// This file was generated by `swagger generate operation`
//
// See hack/swagger-gen.sh
// ----------------------------------------------------------------------------

// ContainerCreateCreatedBody container create created body
// swagger:model ContainerCreateCreatedBody
type ContainerCreateCreatedBody struct {

	// The ID of the created container
	// Required: true
	ID string `json:"Id"`

	// Warnings encountered when creating the container
	// Required: true
	Warnings []string `json:"Warnings"`
}
