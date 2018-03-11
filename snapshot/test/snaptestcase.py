# Copyright 2017 The Kubernetes Authors.
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

import unittest
import os
import subprocess

kubesrcdir = None

class SnapTestCase(unittest.TestCase):
    testdir = os.path.abspath(os.path.dirname(__file__))

    @classmethod
    def kubectl(self, kubeargs, ignore_error=False):
        """
        Shotrcut for kubectl process spawning
        If ingnore_error is True tests will not fail on nonzero kubectl result
        """
        kube_command = kubesrcdir + '/cluster/kubectl.sh ' + kubeargs
        print("Runnning ", kube_command)
        res = subprocess.Popen(kube_command, shell=True, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE)

        out, _err = res.communicate()
        retval = res.returncode
        if not ignore_error and retval != 0:
            raise AssertionError("kubectl returned nonzero exit code")
        return (retval, out.decode().strip())

    def findfile(self, filename):
        """
        Returns full path to a file named filename in the testing directory
        """
        return os.path.join(self.testdir, filename)
