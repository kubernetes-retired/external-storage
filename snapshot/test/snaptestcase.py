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
