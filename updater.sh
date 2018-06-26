#!/bin/bash
gox -output="release/{{.Dir}}_{{.OS}}_{{.Arch}}"
