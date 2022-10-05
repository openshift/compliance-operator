#!/usr/bin/env bash

BUILD_PATH=images/testcontent
DOCKERFILE=${BUILD_PATH}/broken-content.Dockerfile

declare -a tags=(
    'unexistent_resource'
    'proff_diff_baseline'
    'proff_diff_mod'
    'rem_mod_base'
    'rem_mod_change'
    'broken_os_detection'
    'from'
    'to'
    'kubeletconfig'
    'variabletemplate'
    'kubelet_default'
)

CMD=$1
IMAGE_NAME=$2

if [[ "$CMD" != "build" ]] && [[ "$CMD" != "push" ]]; then
    echo "Invalid command '$CMD', it should be 'build' or 'push'"
    exit 1
fi

for tag in "${tags[@]}"
do
    if [[ "$CMD" == "build" ]]; then
        ${RUNTIME} build -t ${IMAGE_NAME}:${tag} \
                         --build-arg xml_path=${BUILD_PATH}/${tag} \
                         -f ${DOCKERFILE} .
    else
        ${RUNTIME} push ${IMAGE_NAME}:${tag}
    fi
done
if [[ "$CMD" == "push" ]]; then
    ${RUNTIME} push ${IMAGE_NAME}:unexistent_resource ${IMAGE_NAME}:latest
fi
