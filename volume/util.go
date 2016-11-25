/*
Copyright 2016 The Kubernetes Authors.

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

package volume

import (
	"io/ioutil"
	"math"
	"os"
	"strings"
	"sync"
)

// generateId generates a unique exportId to assign an export
func generateId(mutex *sync.Mutex, ids map[uint16]bool) uint16 {
	mutex.Lock()
	id := uint16(1)
	for ; id <= math.MaxUint16; id++ {
		if _, ok := ids[id]; !ok {
			break
		}
	}
	ids[id] = true
	mutex.Unlock()
	return id
}

func deleteId(mutex *sync.Mutex, ids map[uint16]bool, id uint16) {
	mutex.Lock()
	delete(ids, id)
	mutex.Unlock()
}

func addToFile(mutex *sync.Mutex, path string, toAdd string) error {
	mutex.Lock()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		mutex.Unlock()
		return err
	}
	defer file.Close()

	if _, err = file.WriteString(toAdd); err != nil {
		mutex.Unlock()
		return err
	}
	file.Sync()

	mutex.Unlock()
	return nil
}

func removeFromFile(mutex *sync.Mutex, path string, toRemove string) error {
	mutex.Lock()

	read, err := ioutil.ReadFile(path)
	if err != nil {
		mutex.Unlock()
		return err
	}

	removed := strings.Replace(string(read), toRemove, "", -1)
	err = ioutil.WriteFile(path, []byte(removed), 0)
	if err != nil {
		mutex.Unlock()
		return err
	}

	mutex.Unlock()
	return nil
}
