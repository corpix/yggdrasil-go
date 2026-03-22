{
  inputs = {
    nixpkgs.url = "tarball+https://git.tatikoma.dev/corpix/nixpkgs/archive/v2025-08-23.848070.tar.gz";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { nixpkgs, flake-utils, ... }: let
    eachSystem = flake-utils.lib.eachSystem flake-utils.lib.allSystems;
  in eachSystem
    (arch: let
      pkgs = import nixpkgs {
        system = arch;
        config = {
          allowUnfree = true;
          android_sdk.accept_license = true;
        };
      };

      inherit (pkgs)
        buildGoModule
        mkShell
        fetchurl
      ;
      inherit (pkgs.lib)
        attrValues
      ;

      goverter = buildGoModule rec {
        pname = "goverter";
        version = "1.8.0";

        src = pkgs.fetchFromGitHub {
          owner = "jmattheis";
          repo = "goverter";
          rev = "v${version}";
          hash = "sha256-wjju0wmtwzWclBPYUYpbXSMAzcLR8R+6hBvnUFSls2E=";
        };
        vendorHash = "sha256-YOtcidMhtQqw/KxY1R3L3XnrhayGQBvHkRdbvYyCQFM=";
        ldflags = [ "-s" "-w" ];
        subPackages = [ "cmd/goverter" ];
      };
      golangci-lint-analyzers = buildGoModule {
        name = "golangci-lint-analyzers";

        src = pkgs.fetchFromGitea {
          domain = "git.tatikoma.dev";
          owner = "corpix";
          repo = "golangci-lint-analyzers";
          rev = "4253a6fd67a7af246573c3c36a9aa277e1b194a4";
          hash = "sha256-5eC/3FgUlIYWxGMPIiN5j73IVkeJK5WEOcV/OjurTpY=";
        };
        vendorHash = null;
        ldflags = [ "-s" "-w" ];
        subPackages = [ "cmd/exhaustive" ];
        doCheck = false;
      };

      gotools = pkgs.gotools.overrideAttrs (oldAttrs: {
        patches = (oldAttrs.patches or []) ++ [
          (fetchurl {
            # https://github.com/golang/go/issues/64271
            # required for ci to properly check imports fmt
            url = "https://patch-diff.githubusercontent.com/raw/golang/tools/pull/554.patch";
            hash = "sha256-I3pQSDi1JYRZ55SDtNXoA3u6Aqd3qrx+PBeLPe4EGQE=";
          })
        ];
      });

      hivemind = pkgs.hivemind.overrideAttrs (oldAttrs: {
        patches = (oldAttrs.patches or []) ++ [
          (fetchurl {
            # passthrough first exited process exit code
            url = "https://git.tatikoma.dev/corpix/hivemind/commit/e561020e2229763be2f17475929a79e49a0f129d.patch";
            hash = "sha256-Jo75rZCnnsba7q5LiFteIuBwM301Q4fI9yBNRhbTfMo=";
          })
        ];
      });

      envPackages = attrValues {
        inherit (pkgs)
          coreutils tree util-linux
          git
          jq yq-go hjson-go
          gcc pkg-config gnumake just
          go gopls delve golangci-lint gofumpt
          python3
          openssl netcat
          gettext

          clang-tools
        ;
        inherit
          hivemind
          gotools
          goverter
          golangci-lint-analyzers
        ;
      };
    in {
      packages.default = buildGoModule {
        name = "yggdrasil";
        src = ./.;
        vendorHash = null;
      };
      devShells.default = mkShell {
        name = "yggdrasil";
        packages = envPackages;
        shellHook = ''
          export GOTELEMETRY=off
          export GOPRIVATE=git.tatikoma.dev/
          export GOSUMDB=off
          export GOPROXY=https://goproxy.tatikoma.dev

          export NIX_PATH=nixpkgs=${nixpkgs}

          # note: in nix shell $0 will point to tmp file
          if test -t 0 && ! test -f "$0"
          then
            exec fish -C 'source env.fish' -i
          fi
        '';
      };
    });
}
