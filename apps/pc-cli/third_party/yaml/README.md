# Vendored: go-yaml/yaml v3.0.1

Upstream: https://github.com/go-yaml/yaml (module path `gopkg.in/yaml.v3`)
Version: v3.0.1
License: MIT and Apache-2.0 (see LICENSE and NOTICE in this directory)

Why vendored: the App Map format is PhoneGo's YAML, and this workstation's Go
module cache has no YAML package. `GOPROXY=off` builds must not fetch one, so the
package is committed here and imported as `chopin/third_party/yaml`.

Only non-test upstream files are included; `*_test.go` files were not copied.
