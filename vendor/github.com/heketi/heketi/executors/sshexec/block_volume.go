//
// Copyright (c) 2017 The heketi Authors
//
// This file is licensed to you under your choice of the GNU Lesser
// General Public License, version 3 or any later version (LGPLv3 or
// later), or the GNU General Public License, version 2 (GPLv2), in all
// cases as published by the Free Software Foundation.
//

package sshexec

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/heketi/heketi/executors"
	"github.com/lpabon/godbc"
)

func (s *SshExecutor) BlockVolumeCreate(host string,
	volume *executors.BlockVolumeRequest) (*executors.BlockVolumeInfo, error) {

	godbc.Require(volume != nil)
	godbc.Require(host != "")
	godbc.Require(volume.Name != "")

	type CliOutput struct {
		Iqn      string   `json:"IQN"`
		Username string   `json:"USERNAME"`
		Password string   `json:"PASSWORD"`
		Portal   []string `json:"PORTAL(S)"`
		Result   string   `json:"RESULT"`
		ErrCode  int      `json:"errCode"`
		ErrMsg   string   `json:"errMsg"`
	}

	var auth_set string
	if volume.Auth {
		auth_set = "enable"
	} else {
		auth_set = "disable"
	}

	cmd := fmt.Sprintf("gluster-block create %v/%v  ha %v auth %v prealloc full %v %vG --json",
		volume.GlusterVolumeName, volume.Name, volume.Hacount, auth_set, strings.Join(volume.BlockHosts, ","), volume.Size)

	// Initialize the commands with the create command
	commands := []string{cmd}

	// Execute command
	output, err := s.RemoteExecutor.RemoteCommandExecute(host, commands, 10)
	if err != nil {
		s.BlockVolumeDestroy(host, volume.GlusterVolumeName, volume.Name)
		return nil, err
	}

	var blockVolumeCreate CliOutput
	err = json.Unmarshal([]byte(output[0]), &blockVolumeCreate)
	if err != nil {
		return nil, fmt.Errorf("Unable to get the block volume create info for block volume %v", volume.Name)
	}

	if blockVolumeCreate.Result == "FAIL" {
		s.BlockVolumeDestroy(host, volume.GlusterVolumeName, volume.Name)
		logger.LogError("%v", blockVolumeCreate.ErrMsg)
		return nil, fmt.Errorf("%v", blockVolumeCreate.ErrMsg)
	}

	var blockVolumeInfo executors.BlockVolumeInfo

	blockVolumeInfo.BlockHosts = volume.BlockHosts // TODO: split blockVolumeCreate.Portal into here instead of using request data
	blockVolumeInfo.GlusterNode = volume.GlusterNode
	blockVolumeInfo.GlusterVolumeName = volume.GlusterVolumeName
	blockVolumeInfo.Hacount = volume.Hacount
	blockVolumeInfo.Iqn = blockVolumeCreate.Iqn
	blockVolumeInfo.Name = volume.Name
	blockVolumeInfo.Size = volume.Size
	blockVolumeInfo.Username = blockVolumeCreate.Username
	blockVolumeInfo.Password = blockVolumeCreate.Password

	return &blockVolumeInfo, nil
}

func (s *SshExecutor) BlockVolumeDestroy(host string, blockHostingVolumeName string, blockVolumeName string) error {
	godbc.Require(host != "")
	godbc.Require(blockHostingVolumeName != "")
	godbc.Require(blockVolumeName != "")

	commands := []string{
		fmt.Sprintf("gluster-block delete %v/%v --json", blockHostingVolumeName, blockVolumeName),
	}

	type CliOutput struct {
		Result       string `json:"RESULT"`
		ResultOnHost string `json:"Result"`
		ErrCode      int    `json:"errCode"`
		ErrMsg       string `json:"errMsg"`
	}
	output, err := s.RemoteExecutor.RemoteCommandExecute(host, commands, 10)
	if err != nil {
		logger.LogError("Unable to delete volume %v: %v", blockVolumeName, err)
		return err
	}

	var blockVolumeDelete CliOutput
	err = json.Unmarshal([]byte(output[0]), &blockVolumeDelete)
	if err != nil {
		err := logger.LogError("Unable to get the block volume delete info for block volume %v", blockVolumeName)
		return err
	}

	if blockVolumeDelete.Result == "FAIL" {
		err := logger.LogError("%v", blockVolumeDelete.ErrMsg)
		return err
	}

	return nil
}
