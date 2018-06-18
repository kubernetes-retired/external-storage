package testing

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/gophercloud/gophercloud/openstack/loadbalancer/v2/l7policies"
	th "github.com/gophercloud/gophercloud/testhelper"
	"github.com/gophercloud/gophercloud/testhelper/client"
)

// SingleL7PolicyBody is the canned body of a Get request on an existing l7policy.
const SingleL7PolicyBody = `
{
	"l7policy": {
		"listener_id": "023f2e34-7806-443b-bfae-16c324569a3d",
		"description": "",
		"admin_state_up": true,
		"redirect_pool_id": null,
		"redirect_url": "http://www.example.com",
		"action": "REDIRECT_TO_URL",
		"position": 1,
		"tenant_id": "e3cd678b11784734bc366148aa37580e",
		"id": "8a1412f0-4c32-4257-8b07-af4770b604fd",
		"name": "redirect-example.com",
		"rules": []
	}
}
`

var (
	L7PolicyToURL = l7policies.L7Policy{
		ID:             "8a1412f0-4c32-4257-8b07-af4770b604fd",
		Name:           "redirect-example.com",
		ListenerID:     "023f2e34-7806-443b-bfae-16c324569a3d",
		Action:         "REDIRECT_TO_URL",
		Position:       1,
		Description:    "",
		TenantID:       "e3cd678b11784734bc366148aa37580e",
		RedirectPoolID: "",
		RedirectURL:    "http://www.example.com",
		AdminStateUp:   true,
		Rules:          []l7policies.Rule{},
	}
)

// HandleL7PolicyCreationSuccessfully sets up the test server to respond to a l7policy creation request
// with a given response.
func HandleL7PolicyCreationSuccessfully(t *testing.T, response string) {
	th.Mux.HandleFunc("/v2.0/lbaas/l7policies", func(w http.ResponseWriter, r *http.Request) {
		th.TestMethod(t, r, "POST")
		th.TestHeader(t, r, "X-Auth-Token", client.TokenID)
		th.TestJSONRequest(t, r, `{
			"l7policy": {
				"listener_id": "023f2e34-7806-443b-bfae-16c324569a3d",
				"redirect_url": "http://www.example.com",
				"name": "redirect-example.com",
				"action": "REDIRECT_TO_URL"
			}
		}`)

		w.WriteHeader(http.StatusAccepted)
		w.Header().Add("Content-Type", "application/json")
		fmt.Fprintf(w, response)
	})
}
