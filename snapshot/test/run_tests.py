#!/usr/bin/python3
import argparse
import unittest
import snaptestcase
import sys
import os
import subprocess
import time
import atexit

def reap_process(proc):
    proc.terminate()
    proc.wait()

if __name__ == '__main__':
    suite = unittest.TestSuite()
    kubelog = sys.stdout
    ctrllog = sys.stdout
    provlog = sys.stdout

    argparser = argparse.ArgumentParser(description='Volume Snapshotter test suite')
    argparser.add_argument('-n', '--no-kubernetes', dest='nokube',
                           help='don\'t start the kubernetes daemon')
    argparser.add_argument('-k', '--kubelog', dest='kubelog',
                           help='file to log kubernetes messaes to (default stdout)')
    argparser.add_argument('-c', '--ctrllog', dest='ctrllog',
                           help='file to log controller messaes to (default stdout)')
    argparser.add_argument('-p', '--provlog', dest='provlog',
                           help='file to log provisioner messaes to (default stdout)')
    argparser.add_argument('testname', nargs='*',
                           help='name of test class or method (e. g. "Hostpath")')
    args = argparser.parse_args()

    testdir = os.path.abspath(os.path.dirname(__file__))
    snapsrcdir = os.path.abspath(os.path.join(testdir, '..'))
    kubesrcdir = os.getenv('KUBESRCDIR', default=os.path.join(os.getenv('HOME'), 'kubernetes'))
    snaptestcase.kubesrcdir = kubesrcdir


    # find the kubernetes dir an start the cluster (if not disabled on commandline)
    if not args.nokube:
        if args.kubelog:
            kubelog = open(args.kubelog, mode='w')
        kubecmd = os.path.join(kubesrcdir, 'hack/local-up-cluster.sh')
        # start the cluster
        kubeoutputdir = os.path.join(kubesrcdir, '_output/local/bin/linux/amd64')
        print("Starting " + kubecmd)
        kube = subprocess.Popen([kubecmd + ' -o ' + kubeoutputdir],
                shell=True, stdout=kubelog, stderr=kubelog, cwd=kubesrcdir)
        # give the cluster some time to initialize
        time.sleep(20)
        kube.poll()
        if kube.returncode != None:
            print("Fatal: Unable to start the kubernetes cluster", file=sys.stderr)
            sys.exit(1)
        atexit.register(reap_process, kube)

    kubeconfig = os.getenv('KUBECONFIG', '/var/run/kubernetes/admin.kubeconfig')
    # start the controller
    if args.ctrllog:
        ctrllog = open(args.ctrllog, mode='w')
    ctrlcmd = os.path.join(snapsrcdir, '_output/bin/snapshot-controller')
    ctrl = subprocess.Popen([ctrlcmd + ' -v 10 -alsologtostderr -kubeconfig ' + kubeconfig],
            shell=True, stdout=ctrllog, stderr=ctrllog, cwd=snapsrcdir)
    time.sleep(10)
    ctrl.poll()
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
    time.sleep(10)
    prov.poll()
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
