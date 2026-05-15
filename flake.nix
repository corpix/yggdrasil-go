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

      bomutils = pkgs.bomutils.overrideAttrs (old: {
        src = pkgs.fetchFromGitHub {
          owner = "hogliux";
          repo  = "bomutils";
          rev   = "c247002e2f49878bab32dd0fcf805810ae0992cb";
          hash  = "sha256-CtRDkx/BmlkIQL0qz6yNgdkM2FZWYSTB7lTRmM6zmXE=";
        };
      });

      codeql = pkgs.codeql.overrideAttrs (old: {
        nativeBuildInputs = (old.nativeBuildInputs or []) ++ [ pkgs.autoPatchelfHook ];

        buildInputs = (old.buildInputs or []) ++ (with pkgs; [
          zlib
          libx11
          libxext
          libxi
          libXtst
          libXrender
          freetype
          jdk17
          curl
          stdenv.cc.cc.lib
          alsa-lib
          lttng-ust_2_12
        ]);
      });

      packages = (with pkgs; [
        coreutils tree util-linux
        git
        jq yq-go hjson-go
        gcc pkg-config gnumake just
        go gopls delve golangci-lint gofumpt gotools
        python3
        openssl netcat
        gettext

        clang-tools
        msitools
        upx
        curl
        cpio
        xar
      ]) ++ [ 
        codeql
        bomutils 
      ];
    in {
      packages.default = buildGoModule {
        name = "yggdrasil";
        src = ./.;
        vendorHash = null;
      };
      devShells.default = mkShell {
        name = "yggdrasil";
        inherit packages;
        shellHook = ''
          export GOTELEMETRY=off
          export GOPRIVATE=git.tatikoma.dev/
          export GOSUMDB=off
          export GOPROXY=https://goproxy.tatikoma.dev
          export NIX_PATH=nixpkgs=${nixpkgs}
        '';
      };
    });
}
