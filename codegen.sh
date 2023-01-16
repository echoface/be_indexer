#!/bin/bash

if [ ! -d codegen/cache ]; then
    mkdir -p codegen/cache
    echo "create codegen/cache"
fi

PROJDIR=`pwd`
protoc -I=${PROJDIR}/idl/proto --go_out=codegen ${PROJDIR}/idl/proto/cache.proto


