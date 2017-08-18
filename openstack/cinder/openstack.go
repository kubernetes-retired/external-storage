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


package main

import (
	"crypto/tls"
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

type Config struct {
	Global struct {
		CinderEndpoint  string `gcfg:"cinder-endpoint"`
		AuthUrl         string `gcfg:"auth-url"`
		Username        string
		UserId          string `gcfg:"user-id"`
		Password        string
		TenantId        string `gcfg:"tenant-id"`
		TenantName      string `gcfg:"tenant-name"`
		TrustId         string `gcfg:"trust-id"`
		DomainId        string `gcfg:"domain-id"`
		DomainName      string `gcfg:"domain-name"`
		Region          string
		CAFile          string `gcfg:"ca-file"`
       }
}


func (cfg Config) toAuthOptions() gophercloud.AuthOptions {
	return gophercloud.AuthOptions{
		IdentityEndpoint: cfg.Global.AuthUrl,
		Username:         cfg.Global.Username,
		UserID:           cfg.Global.UserId,
		Password:         cfg.Global.Password,
		TenantID:         cfg.Global.TenantId,
		TenantName:       cfg.Global.TenantName,
		DomainID:         cfg.Global.DomainId,
		DomainName:       cfg.Global.DomainName,

		// Persistent service, so we need to be able to renew tokens.
		AllowReauth: true,
	}
}


func (cfg Config) toAuth3Options() tokens3.AuthOptions {
	return tokens3.AuthOptions{
		IdentityEndpoint: cfg.Global.AuthUrl,
		Username:         cfg.Global.Username,
		UserID:           cfg.Global.UserId,
		Password:         cfg.Global.Password,
		DomainID:         cfg.Global.DomainId,
		DomainName:       cfg.Global.DomainName,
		AllowReauth:      true,
	}
}


func getConfig(configFilePath string) (Config, error) {
	if configFilePath != "" {
		var configFile *os.File
		var config Config
		configFile, err := os.Open(configFilePath)
		if err != nil {
			glog.Fatalf("Couldn't open configuration %s: %#v",
				configFilePath, err)
			return Config{}, err
		}

		defer configFile.Close()

		err = gcfg.ReadInto(&config, configFile)
		if err != nil {
			glog.Fatalf("Couldn't read configuration: %#v", err)
			return Config{}, err
		}
		return config, nil

	} else {
		// Pass explicit nil so plugins can actually check for nil. See
		// "Why is my nil error value not equal to nil?" in golang.org/doc/faq.
		glog.Fatal("No config file path specified")
		return Config{}, errors.New("Missing configuration")
	}
}


func getStandaloneVolumeService(cfg Config) (*gophercloud.ServiceClient, error) {
	opts := gophercloud.NoAuthOptions{
		Username:   cfg.Global.Username,
		TenantName: cfg.Global.TenantName,
	}
	provider, err := openstack.UnAuthenticatedClient(opts)
	if err != nil {
		glog.Fatalf("Unable to initialize noauth client: %#v", err)
		return nil, err
	}

	volumeService, err := openstack.NewBlockStorageV2(provider, gophercloud.EndpointOpts{
		Region: cfg.Global.Region,
		CinderEndpoint: cfg.Global.CinderEndpoint,
	})
	if err != nil {
		glog.Fatalf("Unable to get volume service: %#v", err)
		return nil, err
	}
	return volumeService, nil
}


func getKeystoneVolumeService(cfg Config) (*gophercloud.ServiceClient, error) {
	provider, err := openstack.NewClient(cfg.Global.AuthUrl)
	if err != nil {
		return nil, err
	}
	if cfg.Global.CAFile != "" {
		roots, err := certutil.NewPool(cfg.Global.CAFile)
		if err != nil {
			return nil, err
		}
		config := &tls.Config{}
		config.RootCAs = roots
		provider.HTTPClient.Transport = netutil.SetOldTransportDefaults(&http.Transport{TLSClientConfig: config})

	}
	if cfg.Global.TrustId != "" {
		opts := cfg.toAuth3Options()
		authOptsExt := trusts.AuthOptsExt{
			TrustID:            cfg.Global.TrustId,
			AuthOptionsBuilder: &opts,
		}
		err = openstack.AuthenticateV3(provider, authOptsExt, gophercloud.EndpointOpts{})
	} else {
		err = openstack.Authenticate(provider, cfg.toAuthOptions())
	}

	if err != nil {
		return nil, err
	}

	volumeService, err := openstack.NewBlockStorageV2(provider,
		gophercloud.EndpointOpts{
			Region: cfg.Global.Region,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to get volume service: %v", err)
	}
	return volumeService, nil
}


func getVolumeService(configFilePath string) (*gophercloud.ServiceClient, error) {
	config, err := getConfig(configFilePath)
	if err != nil {
		return nil, err
	}

	if config.Global.CinderEndpoint != "" {
		return getStandaloneVolumeService(config)
	} else {
		return getKeystoneVolumeService(config)
	}
}
