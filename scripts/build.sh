#!/usr/bin/env bash
set -e

# try and use the correct MD5 lib (depending on user OS darwin/linux)
MD5=$(which md5 || which md5sum)

# for versioning
getCurrCommit() {
  echo `git rev-parse --short HEAD| tr -d "[ \r\n\']"`
}

# for versioning
getCurrTag() {
  echo `git describe --always --tags --abbrev=0 | tr -d "[v\r\n]"`
}

# remove any previous builds that may have failed
[ -e "./build" ] && \
  echo "Cleaning up old builds..." && \
  rm -rf "./build"

# build portal
echo "Building portal..."
# export GOROOT="/usr/local/go-1.7.6"
# export PATH=/usr/local/go-1.7.6/bin:$PATH

# should be built with go1.7.x until tls regression is resolved. also https://github.com/golang/go/issues/21133
gox -ldflags="-s -X github.com/mu-box/portal/commands.tag=$(getCurrTag)
  -X github.com/mu-box/portal/commands.commit=$(getCurrCommit)" \
  -osarch "linux/amd64" -output="./build/{{.OS}}/{{.Arch}}/portal"
  # -osarch "darwin/amd64 linux/amd64 windows/amd64" -output="./build/{{.OS}}/{{.Arch}}/portal"

# look through each os/arch/file and generate an md5 for each
echo "Generating md5s..."
for os in $(ls ./build); do
  for arch in $(ls ./build/${os}); do
    for file in $(ls ./build/${os}/${arch}); do
      cat "./build/${os}/${arch}/${file}" | ${MD5} | awk '{print $1}' >> "./build/${os}/${arch}/${file}.md5"
    done
  done
done
