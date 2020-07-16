#!/usr/bin/env python3
import sys
import os

NULL_VERSION = [0,0,0]

MAJOR = 0
MINOR = 1
PATCH = 2

def find_latest_version_dir(root):
    dirs = []
    with os.scandir(root) as it:
        for entry in it:
            if not entry.is_dir():
                continue
            dirs.append(entry.name)
    return find_latest_version(dirs)

def find_latest_version(vlist):
    if len(vlist) < 1:
        return ""
    latest = ""
    for ver in vlist:
        if is_higher(latest, ver):
            latest = ver

    return latest

def is_higher(latest, candidate):
    latest_tuple = parse_semver(latest)
    candidate_tuple = parse_semver(candidate)

    if candidate_tuple[MAJOR] > latest_tuple[MAJOR]:
        return True
    if candidate_tuple[MINOR] > latest_tuple[MINOR]:
        return True
    if candidate_tuple[PATCH] > latest_tuple[PATCH]:
        return True

    return False

def parse_semver(vstr):
    # The operator uses a very basic subset of semver, just major.minor.patch
    semtup = vstr.split('.')

    if len(semtup) != 3:
        return NULL_VERSION

    semints = []
    for item in semtup:
        if item.isdigit() == False:
            return NULL_VERSION
        semints.append(int(item))

    return semints

if __name__ == "__main__":
    if len(sys.argv) <= 1:
        # Since the script is called from Makefile, just return nothing and let
        # make fail
        sys.exit(0)

    latest = find_latest_version_dir(sys.argv[1])
    print(latest)
    sys.exit(0)
