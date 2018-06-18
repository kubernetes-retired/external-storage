package l7policies

import (
	"github.com/gophercloud/gophercloud"
)

// CreateOptsBuilder allows extensions to add additional parameters to the
// Create request.
type CreateOptsBuilder interface {
	ToL7PolicyCreateMap() (map[string]interface{}, error)
}

type Action string
type RuleType string
type CompareType string

const (
	ActionRedirectToPool Action = "REDIRECT_TO_POOL"
	ActionRedirectToURL  Action = "REDIRECT_TO_URL"
	ActionReject         Action = "REJECT"

	TypeCookie   RuleType = "COOKIE"
	TypeFileType RuleType = "FILE_TYPE"
	TypeHeader   RuleType = "HEADER"
	TypeHostName RuleType = "HOST_NAME"
	TypePath     RuleType = "PATH"

	CompareTypeContains  CompareType = "CONTAINS"
	CompareTypeEndWith   CompareType = "ENDS_WITH"
	CompareTypeEqual     CompareType = "EQUAL_TO"
	CompareTypeRegex     CompareType = "REGEX"
	CompareTypeStartWith CompareType = "STARTS_WITH"
)

// CreateOpts is the common options struct used in this package's Create
// operation.
type CreateOpts struct {
	// Name of the L7 policy.
	Name string `json:"name,omitempty"`

	// The ID of the listener.
	ListenerID string `json:"listener_id" required:"true"`

	// The L7 policy action. One of REDIRECT_TO_POOL, REDIRECT_TO_URL, or REJECT.
	Action Action `json:"action" required:"true"`

	// The position of this policy on the listener.
	Position int32 `json:"position,omitempty"`

	// A human-readable description for the resource.
	Description string `json:"description,omitempty"`

	// TenantID is the UUID of the project who owns the L7 policy in neutron-lbaas.
	// Only administrative users can specify a project UUID other than their own.
	TenantID string `json:"tenant_id,omitempty"`

	// ProjectID is the UUID of the project who owns the L7 policy in octavia.
	// Only administrative users can specify a project UUID other than their own.
	ProjectID string `json:"project_id,omitempty"`

	// Requests matching this policy will be redirected to the pool with this ID.
	// Only valid if action is REDIRECT_TO_POOL.
	RedirectPoolID string `json:"redirect_pool_id,omitempty"`

	// Requests matching this policy will be redirected to this URL.
	// Only valid if action is REDIRECT_TO_URL.
	RedirectURL string `json:"redirect_url,omitempty"`
}

// ToL7PolicyCreateMap builds a request body from CreateOpts.
func (opts CreateOpts) ToL7PolicyCreateMap() (map[string]interface{}, error) {
	return gophercloud.BuildRequestBody(opts, "l7policy")
}

// Create accepts a CreateOpts struct and uses the values to create a new l7policy.
func Create(c *gophercloud.ServiceClient, opts CreateOptsBuilder) (r CreateResult) {
	b, err := opts.ToL7PolicyCreateMap()
	if err != nil {
		r.Err = err
		return
	}
	_, r.Err = c.Post(rootURL(c), b, &r.Body, nil)
	return
}
