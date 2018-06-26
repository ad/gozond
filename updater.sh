#!/bin/bash
echo "$1"
git tag "$1" && git push --tags
gox -output="release/{{.Dir}}_{{.OS}}_{{.Arch}}" -osarch="darwin/386 darwin/amd64 linux/386 linux/amd64 linux/arm windows/386 windows/amd64"
github-release release --user ad --repo gozond --tag v"$1" --name "$1"
github-release upload --user ad --repo gozond --tag v"$1" --name "gozond_darwin_386" --file release/gozond_darwin_386
github-release upload --user ad --repo gozond --tag v"$1" --name "gozond_darwin_amd64" --file release/gozond_darwin_amd64
github-release upload --user ad --repo gozond --tag v"$1" --name "gozond_linux_386" --file release/gozond_linux_386
github-release upload --user ad --repo gozond --tag v"$1" --name "gozond_linux_amd64" --file release/gozond_linux_amd64
github-release upload --user ad --repo gozond --tag v"$1" --name "gozond_linux_arm" --file release/gozond_linux_arm
github-release upload --user ad --repo gozond --tag v"$1" --name "gozond_windows_386.exe" --file release/gozond_windows_386.exe
github-release upload --user ad --repo gozond --tag v"$1" --name "gozond_windows_amd64.exe" --file release/gozond_windows_amd64.exe
