#!/bin/bash

LATEST_RELEASE="$(gh release list --json name,isLatest --jq '.[] | select(.isLatest)|.name')"
LATEST_RELEASE_MINOR="$(echo "$LATEST_RELEASE" | awk -F "." '{print $2 }')"
NEXT_RELEASE_MINOR="$(($LATEST_RELEASE_MINOR+1))"
NEXT_RELEASE_VERSION="v0.${NEXT_RELEASE_MINOR}.0"
echo "Next release is $NEXT_RELEASE_VERSION"
perl -pi -e "s/\Q$LATEST_RELEASE\E/$NEXT_RELEASE_VERSION/g" RELEASE_PROCESS.md

