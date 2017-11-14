#!/usr/bin/python3
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

import argparse
import unittest
import snaptestcase
import sys
import os
import subprocess
import time
import atexit
import json

def reap_process(proc):
    proc.terminate()
    proc.wait()

if __name__ == '__main__':
    suite = unittest.TestSuite()
    kubelog = sys.stdout
    ctrllog = sys.stdout
    provlog = sys.stdout

    argparser = argparse.ArgumentParser(description='Volume Snapshotter test suite')
    argparser.add_argument('-n', '--no-kubernetes', dest='nokube', action='store_true',
                           help='don\'t start the kubernetes daemon')
    argparser.add_argument('-t', '--tests-only', dest='testsonly', action='store_true',
                           help='don\'t start anything, just do the tests')
    argparser.add_argument('-k', '--kubelog', dest='kubelog',
                           help='file to log kubernetes messaes to (default stdout)')
    argparser.add_argument('-c', '--ctrllog', dest='ctrllog',
                           help='file to log controller messaes to (default stdout)')
    argparser.add_argument('-p', '--provlog', dest='provlog',
                           help='file to log provisioner messaes to (default stdout)')
    argparser.add_argument('-d', '--deployment', dest='deployment',
                           help='path of deployment yaml file; uses containers for test')
    argparser.add_argument('-r', '--rbac', dest='rbac',
                           help='path of rbac definitions yaml file for deployment')
    argparser.add_argument('testname', nargs='*',
                           help='name of test class or method (e. g. "Hostpath")')
    args = argparser.parse_args()

    testdir = os.path.abspath(os.path.dirname(__file__))
    snapsrcdir = os.path.abspath(os.path.join(testdir, '..'))
    kubesrcdir = os.getenv('KUBESRCDIR', default=os.path.join(os.getenv('HOME'), 'kubernetes'))
    snaptestcase.kubesrcdir = kubesrcdir

    kubectl = 'kubectl'
    if not args.testsonly:
        # find the kubernetes dir an start the cluster (if not disabled on commandline)
        if not args.nokube:
            if args.kubelog:
                kubelog = open(args.kubelog, mode='w')
            kubectl = os.path.join(kubesrcdir, "cluster/kubectl.sh")
            kubecmd = os.path.join(kubesrcdir, 'hack/local-up-cluster.sh')
            # start the cluster
            kubeoutputdir = os.path.join(kubesrcdir, '_output/local/bin/linux/amd64')
            print("Starting " + kubecmd)
            kube = subprocess.Popen([kubecmd + ' -o ' + kubeoutputdir],
                    shell=True, stdout=kubelog, stderr=kubelog, cwd=kubesrcdir)
            # give the cluster some time to initialize
            check = subprocess.Popen([kubectl + ' get nodes'], shell=True)
            check.wait()
            check_count = 0
            while check.returncode != 0 and check_count < 30:
                time.sleep(1)
                check = subprocess.Popen([kubectl + ' get nodes'], shell=True)
                check.wait()
                check_count = check_count + 1
            nodes_num = 0
            check_count = 0
            while nodes_num == 0 and check_count < 30:
                time.sleep(1)
                check_count = check_count + 1
                check = subprocess.check_output([kubectl + ' get nodes -o json'], shell=True)
                try:
                    node_list = json.loads(check.decode("utf-8"))
                    nodes_num = len(node_list['items'])
                except Exception as e:
                    pass

            kube.poll()
            if kube.returncode != None:
                print("Fatal: Unable to start the kubernetes cluster", file=sys.stderr)
                sys.exit(1)
            atexit.register(reap_process, kube)
        if args.deployment:
            deployment_file = os.path.abspath(args.deployment)
            rbac_file = os.path.abspath(args.rbac)
            new_rbac = subprocess.Popen([kubectl + ' create -f ' + rbac_file],
                    shell=True, stdout=kubelog, stderr=kubelog)
            new_rbac.wait()
            if new_rbac.returncode != 0:
                print("Error creating the snapshot deployment rbac", file=sys.stderr)
                sys.exit(1)
            new_depl = subprocess.Popen([kubectl + ' create -f ' + deployment_file],
                    shell=True, stdout=kubelog, stderr=kubelog)
            new_depl.wait()
            if new_depl.returncode != 0:
                print("Error creating the snapshot deployment", file=sys.stderr)
                sys.exit(1)
            ready_replicas = 0
            check_count = 0
            while ready_replicas == 0 and check_count < 30:
                time.sleep(1)
                check_count = check_count + 1
                check = subprocess.check_output([kubectl + ' get deployments -o json'], shell=True)
                try:
                    deployment_list = json.loads(check.decode("utf-8"))
                    if len(deployment_list['items']) > 0:
                        ready_replicas = deployment_list['items'][0]['status']['readyReplicas']
                except Exception as e:
                    pass

        else:
            kubeconfig = os.getenv('KUBECONFIG', '/var/run/kubernetes/admin.kubeconfig')
            # start the controller
            if args.ctrllog:
                ctrllog = open(args.ctrllog, mode='w')
            ctrlcmd = os.path.join(snapsrcdir, '_output/bin/snapshot-controller')
            ctrl = subprocess.Popen([ctrlcmd + ' -v 10 -alsologtostderr -kubeconfig ' + kubeconfig],
                    shell=True, stdout=ctrllog, stderr=ctrllog, cwd=snapsrcdir)
            ctrl.wait()
            if ctrl.returncode != None:
                print("Fatal: Unable to start the snapshot controller", file=sys.stderr)
                sys.exit(1)
            atexit.register(reap_process, ctrl)
            # start the provisioner
            if args.provlog:
                ctrllog = open(args.provlog, mode='w')
            provcmd = os.path.join(snapsrcdir, '_output/bin/snapshot-provisioner')
            prov = subprocess.Popen([provcmd + ' -v 10 -alsologtostderr -kubeconfig ' + kubeconfig],
                    shell=True, stdout=provlog, stderr=provlog, cwd=snapsrcdir)
            prov.wait()
            if prov.returncode != None:
                print("Fatal: Unable to start the snapshot provisioner", file=sys.stderr)
                sys.exit(1)
            atexit.register(reap_process, prov)

    # Load all files in this directory whose name starts with 'test'
    if args.testname:
        for n in args.testname:
            suite.addTests(unittest.TestLoader().loadTestsFromName(n))
    else:
        for test_cases in unittest.defaultTestLoader.discover(testdir):
            suite.addTest(test_cases)
    result = unittest.TextTestRunner(verbosity=2).run(suite)

    if result.wasSuccessful():
        sys.exit(0)
    else:
        sys.exit(1)
