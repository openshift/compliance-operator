#!/bin/bash -e
# usage: ./update_changelog.sh <release number>
release=$1
cl=CHANGELOG.md
today=$(date +%F)
header=$(head -n7 ${cl})
older=$(tail -n+7 ${cl})

# get all commits on head since the latest tag
changes=$(git log --no-merges --pretty=format:"- %s" $(git describe --tags --abbrev=0 HEAD^)..HEAD)

echo "${header}" > ${cl}
echo "" >> ${cl}
echo "## [${release}] - ${today}" >> ${cl}
echo "### Changes" >> ${cl}
echo "${changes}" >> ${cl}
echo "${older}" >> ${cl}
