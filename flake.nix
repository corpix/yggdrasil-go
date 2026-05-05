{
  inputs = {
    nixpkgs.url = "tarball+https://git.tatikoma.dev/corpix/nixpkgs/archive/corpix.tar.gz";
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
