# Build yggdrasil and yggdrasilctl
# Usage examples:
#   just build
#   just build debug=1
#   just build race=1 pie=1
#   just build output=./bin/yggdrasilctl
#   just build ldflags='-X main.foo=bar' gcflags='-N -l'
#   just build upx=1
build debug="0" race="0" pie="0" tables="0" upx="0" output="" ldflags="" gcflags="":
    #!/usr/bin/env bash
    set -eu -o pipefail

    : "${PKGSRC:=github.com/yggdrasil-network/yggdrasil-go/src/version}"
    PKGNAME="$(sh contrib/semver/name.sh)"
    PKGVER="$(sh contrib/semver/version.sh --bare)"
    LDFLAGS="-X $PKGSRC.buildName=$PKGNAME -X $PKGSRC.buildVersion=$PKGVER"
    ARGS="-v"
    GCFLAGS=""

    if [ "{{debug}}" = "1" ]; then
      ARGS="$ARGS -tags debug"
    fi
    if [ "{{race}}" = "1" ]; then
      ARGS="$ARGS -race"
    fi
    if [ "{{pie}}" = "1" ]; then
      ARGS="$ARGS -buildmode=pie"
    fi
    if [ -n "{{output}}" ]; then
      ARGS="$ARGS -o {{output}}"
    fi
    if [ -n "{{gcflags}}" ]; then
      GCFLAGS="{{gcflags}}"
    fi
    if [ -n "{{ldflags}}" ]; then
      LDFLAGS="$LDFLAGS {{ldflags}}"
    fi
    if [ "{{tables}}" != "1" ] && [ "{{debug}}" != "1" ]; then
      LDFLAGS="$LDFLAGS -s -w"
    fi

    for CMD in yggdrasil yggdrasilctl ; do
      echo "Building: $CMD"
      go build $ARGS -ldflags="$LDFLAGS" -gcflags="${GCFLAGS:-}" ./cmd/$CMD

      if [ "{{upx}}" = "1" ]; then
        upx --brute $CMD
      fi
    done

# Destructive clean
clean:
  git clean -dxf
