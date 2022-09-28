#!/bin/bash

# git-remote.sh prints the remote associated with the upstream
# ComplianceAsCode/compliance-operator repository. If it can't determine the
# appropriate remote based on a known URL, it defaults to using "origin", which
# is backwards compatible with the release process.

REMOTE_URL=origin
for REMOTE in `git remote`; do
        URL=`git config --get remote.$REMOTE.url`
        if [[ "$URL" = "https://github.com/ComplianceAsCode/compliance-operator" ]]; then
                REMOTE_URL=$REMOTE
                break
        fi
done
echo $REMOTE_URL
