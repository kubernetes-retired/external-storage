/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package shareservice

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/extensions/trusts"
	tokens3 "github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
	"gopkg.in/gcfg.v1"

	"github.com/golang/glog"
	netutil "k8s.io/apimachinery/pkg/util/net"
	certutil "k8s.io/client-go/util/cert"
)

type manilaConfig struct {
	Global manilaConfigGlobal
	Manila ShareConfig
}

type manilaConfigGlobal struct {
	ManilaEndpoint string `gcfg:"manila-endpoint"`
	AuthURL        string `gcfg:"auth-url"`
	Username       string
	UserID         string `gcfg:"user-id"`
	Password       string
	TenantID       string `gcfg:"tenant-id"`
	TenantName     string `gcfg:"tenant-name"`
	TrustID        string `gcfg:"trust-id"`
	DomainID       string `gcfg:"domain-id"`
	DomainName     string `gcfg:"domain-name"`
	Region         string
	CAFile         string `gcfg:"ca-file"`
}

// ShareConfig is needed configurations to create share
type ShareConfig struct {
	ShareNetworkID string `gcfg:"share-networkid"`
	ShareProtocol  string `gcfg:"share-protocol"`
	ShareType      string `gcfg:"share-type"`
}

func (cfg manilaConfig) toAuthOptions() gophercloud.AuthOptions {
	return gophercloud.AuthOptions{
		IdentityEndpoint: cfg.Global.AuthURL,
		Username:         cfg.Global.Username,
		UserID:           cfg.Global.UserID,
		Password:         cfg.Global.Password,
		TenantID:         cfg.Global.TenantID,
		TenantName:       cfg.Global.TenantName,
		DomainID:         cfg.Global.DomainID,
		DomainName:       cfg.Global.DomainName,

		// Persistent service, so we need to be able to renew tokens.
		AllowReauth: true,
	}
}

func (cfg manilaConfig) toAuth3Options() tokens3.AuthOptions {
	return tokens3.AuthOptions{
		IdentityEndpoint: cfg.Global.AuthURL,
		Username:         cfg.Global.Username,
		UserID:           cfg.Global.UserID,
		Password:         cfg.Global.Password,
		DomainID:         cfg.Global.DomainID,
		DomainName:       cfg.Global.DomainName,
		AllowReauth:      true,
	}
}

func getConfigFromEnv() manilaConfig {
	authURL := os.Getenv("OS_AUTH_URL")
	if authURL == "" {
		glog.Warning("OS_AUTH_URL missing from environment")
		return manilaConfig{}
	}

	return manilaConfig{
		Global: manilaConfigGlobal{
			AuthURL:  authURL,
			Username: os.Getenv("OS_USERNAME"),
			Password: os.Getenv("OS_PASSWORD"), // TODO: Replace with secret
			TenantID: os.Getenv("OS_TENANT_ID"),
			Region:   os.Getenv("OS_REGION_NAME"),
		},
	}
}

func getConfig(configFilePath string) (manilaConfig, error) {
	if configFilePath != "" {
		var configFile *os.File
		var config manilaConfig
		configFile, err := os.Open(configFilePath)
		if err != nil {
			glog.Fatalf("Couldn't open configuration %s: %#v",
				configFilePath, err)
			return manilaConfig{}, err
		}

		defer configFile.Close()

		err = gcfg.ReadInto(&config, configFile)
		if err != nil {
			glog.Fatalf("Couldn't read configuration: %#v", err)
			return manilaConfig{}, err
		}
		return config, nil
	}
	envConfig := getConfigFromEnv()
	if envConfig != (manilaConfig{}) {
		return envConfig, nil
	}

	// Pass explicit nil so plugins can actually check for nil. See
	// "Why is my nil error value not equal to nil?" in golang.org/doc/faq.
	glog.Fatal("Configuration missing: no config file specified and " +
		"environment variables are not set.")
	return manilaConfig{}, errors.New("Missing configuration")
}

func getKeystoneManilaService(cfg manilaConfig) (*gophercloud.ServiceClient, error) {
	provider, err := openstack.NewClient(cfg.Global.AuthURL)
	if err != nil {
		return nil, err
	}
	if cfg.Global.CAFile != "" {
		var roots *x509.CertPool
		roots, err = certutil.NewPool(cfg.Global.CAFile)
		if err != nil {
			return nil, err
		}
		config := &tls.Config{}
		config.RootCAs = roots
		provider.HTTPClient.Transport = netutil.SetOldTransportDefaults(&http.Transport{TLSClientConfig: config})

	}
	if cfg.Global.TrustID != "" {
		opts := cfg.toAuth3Options()
		authOptsExt := trusts.AuthOptsExt{
			TrustID:            cfg.Global.TrustID,
			AuthOptionsBuilder: &opts,
		}
		err = openstack.AuthenticateV3(provider, authOptsExt, gophercloud.EndpointOpts{})
	} else {
		err = openstack.Authenticate(provider, cfg.toAuthOptions())
	}

	if err != nil {
		return nil, err
	}

	manilaService, err := openstack.NewSharedFileSystemV2(provider,
		gophercloud.EndpointOpts{
			Region: cfg.Global.Region,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to get manila service: %v", err)
	}
	return manilaService, nil
}

// GetShareService returns a connected manila client based on configuration
// specified in configFilePath or the environment.
func GetShareService(configFilePath string) (*gophercloud.ServiceClient, error) {
	config, err := getConfig(configFilePath)
	if err != nil {
		return nil, err
	}
	if config.Global.ManilaEndpoint != "" {
		return nil, errors.New("Manila is not yet supported")
	}
	return getKeystoneManilaService(config)
}

// GetShareConfig returns configuration of manila
func GetShareConfig(configFilePath string) (ShareConfig, error) {
	config, err := getConfig(configFilePath)
	if err != nil {
		return ShareConfig{}, err
	}
	return config.Manila, err
}
