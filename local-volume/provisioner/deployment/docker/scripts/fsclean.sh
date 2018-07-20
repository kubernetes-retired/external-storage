#!/bin/bash -e

# Copyright 2018 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Usage:
# $ fsclean.sh 

# Import common functions.
. $(dirname "$0")/common.sh

if [ "$1" == "-h" ]; then
  echo "Usage: $(basename $0)"
  echo "Invokes fsclean on the filesystem directory specified by environment variable LOCAL_PV_FILESYSTEM"
  exit 0
fi

# Validate that we got a valid filesystem directory to cleanup
validateFilesystem

# Remove all contents under directory.
#
# find:
#  -mindetph 1 -maxdepth 1: List first level children only, let `rm` to remove them recursively.
#  -print0: Use NULL to separate filenames. This allows file names that
#  contain newlines or other types of white space to be correctly
#  interpreted by programs that process the find output.
#
# xargs:
#  -0: Input items are terminated by a null character instead of by whitespace.
# 
ionice -c 3 find "$LOCAL_PV_FILESYSTEM" -mindepth 1 -maxdepth 1 -print0 | xargs -0 ionice -c 3 rm -rf
