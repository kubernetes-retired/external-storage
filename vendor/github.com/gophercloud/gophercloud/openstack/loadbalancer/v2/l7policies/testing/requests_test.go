package testing

import (
	"testing"

	"github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/l7policies"
	fake "github.com/gophercloud/gophercloud/openstack/networking/v2/common"
	th "github.com/gophercloud/gophercloud/testhelper"
)

func TestCreateL7Policy(t *testing.T) {
	th.SetupHTTP()
	defer th.TeardownHTTP()
	HandleL7PolicyCreationSuccessfully(t, SingleL7PolicyBody)

	actual, err := l7policies.Create(fake.ServiceClient(), l7policies.CreateOpts{
		Name:        "redirect-example.com",
		ListenerID:  "023f2e34-7806-443b-bfae-16c324569a3d",
		Action:      l7policies.ActionRedirectToURL,
		RedirectURL: "http://www.example.com",
	}).Extract()

	th.AssertNoErr(t, err)
	th.CheckDeepEquals(t, L7PolicyToURL, *actual)
}

func TestRequiredL7PolicyCreateOpts(t *testing.T) {
	// no param specified.
	res := l7policies.Create(fake.ServiceClient(), l7policies.CreateOpts{})
	if res.Err == nil {
		t.Fatalf("Expected error, got none")
	}

	// Action is invalid.
	res = l7policies.Create(fake.ServiceClient(), l7policies.CreateOpts{
		ListenerID: "023f2e34-7806-443b-bfae-16c324569a3d",
		Action:     l7policies.Action("invalid"),
	})
	if res.Err == nil {
		t.Fatalf("Expected error, but got none")
	}
}
