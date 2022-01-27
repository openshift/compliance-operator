#!/bin/bash -e
# usage: ./update_changelog.sh <release number>
release=$1
cl=CHANGELOG.md
today=$(date +%F)

sed -i "s/## Unreleased/## [${release}] - ${today}/g" ${cl}

header=$(head -n7 ${cl})
older=$(tail -n+7 ${cl})

template="""
## Unreleased

### Enhancements

-

### Fixes

-

### Internal Changes

-

### Deprecations

-

### Removals

-

### Security

-
"""

echo "${header}" > ${cl}
echo "${template}" >> ${cl}
echo "${older}" >> ${cl}
