//
// Copyright (c) 2017 The heketi Authors
//
// This file is licensed to you under your choice of the GNU Lesser
// General Public License, version 3 or any later version (LGPLv3 or
// later), or the GNU General Public License, version 2 (GPLv2), in all
// cases as published by the Free Software Foundation.
//

package glusterfs

import (
	"encoding/json"
	"net/http"

	"github.com/boltdb/bolt"
	"github.com/gorilla/mux"
	"github.com/heketi/heketi/pkg/glusterfs/api"
	"github.com/heketi/heketi/pkg/utils"
)

func (a *App) BlockVolumeCreate(w http.ResponseWriter, r *http.Request) {

	var msg api.BlockVolumeCreateRequest
	err := utils.GetJsonFromRequest(r, &msg)
	if err != nil {
		http.Error(w, "request unable to be parsed", 422)
		return
	}

	if msg.Size < 1 {
		http.Error(w, "Invalid volume size", http.StatusBadRequest)
		logger.LogError("Invalid volume size")
		return
	}

	if msg.Size > BlockHostingVolumeSize {
		err := logger.LogError("Default Block Hosting Volume size is less than block volume requested.")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// TODO: factor this into a function (it's also in VolumeCreate)
	// Check that the clusters requested are available
	err = a.db.View(func(tx *bolt.Tx) error {

		// :TODO: All we need to do is check for one instead of gathering all keys
		clusters, err := ClusterList(tx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return err
		}
		if len(clusters) == 0 {
			err := logger.LogError("No clusters configured")
			http.Error(w, err.Error(), http.StatusBadRequest)
			return ErrNotFound
		}

		// Check the clusters requested are correct
		for _, clusterid := range msg.Clusters {
			_, err := NewClusterEntryFromId(tx, clusterid)
			if err != nil {
				err := logger.LogError("Cluster id %v not found", clusterid)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return err
			}
		}

		return nil
	})
	if err != nil {
		return
	}

	blockVolume := NewBlockVolumeEntryFromRequest(&msg)

	// Add device in an asynchronous function
	a.asyncManager.AsyncHttpRedirectFunc(w, r, func() (string, error) {

		logger.Info("Creating block volume %v", blockVolume.Info.Id)
		err := blockVolume.Create(a.db, a.executor, a.allocator)
		if err != nil {
			logger.LogError("Failed to create block volume: %v", err)
			return "", err
		}

		logger.Info("Created block volume %v", blockVolume.Info.Id)

		return "/blockvolumes/" + blockVolume.Info.Id, nil
	})
}

func (a *App) BlockVolumeList(w http.ResponseWriter, r *http.Request) {

	var list api.BlockVolumeListResponse

	err := a.db.View(func(tx *bolt.Tx) error {
		var err error

		list.BlockVolumes, err = BlockVolumeList(tx)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		logger.Err(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Send list back
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(list); err != nil {
		panic(err)
	}
}

func (a *App) BlockVolumeInfo(w http.ResponseWriter, r *http.Request) {

	vars := mux.Vars(r)
	id := vars["id"]

	// Get volume information
	var info *api.BlockVolumeInfoResponse
	err := a.db.View(func(tx *bolt.Tx) error {
		entry, err := NewBlockVolumeEntryFromId(tx, id)
		if err == ErrNotFound {
			http.Error(w, "Id not found", http.StatusNotFound)
			return err
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return err
		}

		info, err = entry.NewInfoResponse(tx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return err
		}

		return nil
	})
	if err != nil {
		return
	}

	// Write msg
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(info); err != nil {
		panic(err)
	}
}

func (a *App) BlockVolumeDelete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	var blockVolume *BlockVolumeEntry
	err := a.db.View(func(tx *bolt.Tx) error {
		var err error
		blockVolume, err = NewBlockVolumeEntryFromId(tx, id)
		if err == ErrNotFound {
			http.Error(w, err.Error(), http.StatusNotFound)
			return err
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return err
		}

		return nil
	})
	if err != nil {
		return
	}

	a.asyncManager.AsyncHttpRedirectFunc(w, r, func() (string, error) {

		err := blockVolume.Destroy(a.db, a.executor)

		// TODO: If it fails for some reason, we will need to add to the DB again
		// or hold state on the entry "DELETING"

		if err != nil {
			logger.LogError("Failed to delete volume %v: %v", blockVolume.Info.Id, err)
			return "", err
		}

		logger.Info("Deleted volume [%s]", id)
		return "", nil
	})
}
