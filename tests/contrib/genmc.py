#!/usr/bin/env python

# This script is useful to prime a cluster with many MCs, in case it's needed
# to stress-test e.g. the api-resource-collector's ability to fetch many large
# objects.
#
# Example usage:
#       python genmc.py --in-file=/usr/share/dict/linux.words --num-objs=100
#
# Note that the MCs are not assigned to any pool so that we avoid rebooting
# the cluster and flooding the MCD.

import argparse
import sys
import urllib.parse

from subprocess import Popen, PIPE


MC_TEMPLATE = ('''apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 75-testmc-{ID}.rule
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
          source: data:,{DATA}
        mode: 0644
        path: /etc/testmc-{ID}.txt
        overwrite: true''')

# When using large size files as input, let's not overflow the maximum size
# that etcd or MCD allow us to use
MAX_SIZE = 100000

def main():
    parser = argparse.ArgumentParser(
        description='Create many MCs based on input file')
    parser.add_argument('--in-file',
                        dest='in_file',
                        default='/usr/share/dict/linux.words',
                        help='Input file for a MC')
    parser.add_argument('--num-objs',
                        dest='num_objs',
                        type=int,
                        default=1,
                        help="how many MCs to generate")
    parser.add_argument('--start-num',
                        dest='start_num',
                        type=int,
                        default=0,
                        help="start with how many MCs, useful for adding")
    parser.add_argument('--urlencode',
                        dest='urlencode',
                        action='store_true',
                        default=True,
                        help='urlencode the infile')
    parser.add_argument('--out-file',
                        dest='out_file',
                        help='where to write the outfile (if num-files is specified, suffix is added)')
    parser.add_argument('--create',
                        dest='create',
                        action='store_true',
                        default=True,
                        help='create the MCs instead of writing them to disk')
    args = parser.parse_args()

    # open and read infile
    content = ""
    with open(args.in_file) as infile:
        content = infile.readlines()
        # urlencode if needed
        if args.urlencode:
            content = urllib.parse.quote(''.join(content))
        # trim the max size after urlencoding which might increase the size
        content = content[:MAX_SIZE]

    for mcindex in range(args.num_objs):
        mcindex = mcindex + args.start_num + 1
        mc = MC_TEMPLATE.format(ID=mcindex, DATA=content)
        if args.create:
          p = Popen(['oc', 'create', '-f', '-'], stdout=PIPE, stdin=PIPE, stderr=PIPE)
          stdout_data, stderr_data = p.communicate(input=bytes(mc, 'utf-8'))
          print(stdout_data.decode().strip())
          if len(stderr_data) > 0:
            print(stderr_data.decode().strip())
        else:
          #   write the template, add mcindex as suffix
          outfile_name = args.out_file + "-" + str(mcindex) + ".yaml"
          with open(outfile_name, "w") as outfile_contents :
              outfile_contents.write(mc)

if __name__ == "__main__":
    rv = main()
    sys.exit(rv)
