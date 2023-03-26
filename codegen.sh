#!/bin/bash

if [ ! -d codegen/cache ]; then
    mkdir -p codegen/cache
    echo "create codegen/cache"
fi

PROJ_DIR=`pwd`
protoc -I="${PROJ_DIR}"/static/idl/proto --go_out=codegen "${PROJ_DIR}"/static/idl/proto/cache.proto

