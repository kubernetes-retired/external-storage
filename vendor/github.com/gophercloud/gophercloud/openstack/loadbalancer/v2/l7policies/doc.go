/*
Package l7policies provides information and interaction with L7Policies and
Rules of the LBaaS v2 extension for the OpenStack Networking service.
Example to Create a L7Policy
	createOpts := l7policies.CreateOpts{
		Name:        "redirect-example.com",
		ListenerID:  "023f2e34-7806-443b-bfae-16c324569a3d",
		Action:      l7policies.ActionRedirectToURL,
		RedirectURL: "http://www.example.com",
	}
	l7policy, err := l7policies.Create(networkClient, createOpts).Extract()
	if err != nil {
		panic(err)
	}
*/
package l7policies
