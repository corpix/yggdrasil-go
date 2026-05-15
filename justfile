branch         := `git symbolic-ref --short HEAD 2>/dev/null || echo master`
branchname     := replace(env_var_or_default("GITHUB_REF_NAME", branch), "/", "")
pkgname        := if branchname == "master" { "yggdrasil" } else { "yggdrasil-" + branchname }
pkgversion     := `git describe --tags --match="v[0-9]*.[0-9]*.[0-9]*" 2>/dev/null | sed 's/^v//; s/-\([0-9]*\)-g[0-9a-f]*/\-\1/' | grep . || echo "0.0.0-$(git rev-list --count HEAD)"`
pkgdisplayname := pkgname + "-" + pkgversion

@pkgname:
    echo {{pkgname}}

@pkgversion:
    echo {{pkgversion}}

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
    LDFLAGS="-X $PKGSRC.buildName={{pkgname}} -X $PKGSRC.buildVersion={{pkgversion}}"
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

build-debian pkgarch="amd64":
    #!/usr/bin/env bash
    set -eu -o pipefail

    ROOTDIR="$(pwd)"
    OUTDIR="$ROOTDIR/build/debian/{{pkgarch}}"
    mkdir -p "$OUTDIR"

    GOARM=""
    case "{{pkgarch}}" in
      amd64)  GOARCH=amd64  ;;
      i386)   GOARCH=386    ;;
      mipsel) GOARCH=mipsle ;;
      mips)   GOARCH=mips64 ;;
      armhf)  GOARCH=arm; GOARM=6 ;;
      arm64)  GOARCH=arm64  ;;
      armel)  GOARCH=arm; GOARM=5 ;;
      *) echo "Unknown pkgarch: {{pkgarch}}"; exit 1 ;;
    esac

    GOLDFLAGS="-X github.com/yggdrasil-network/yggdrasil-go/src/config.defaultConfig=/etc/yggdrasil/yggdrasil.conf"
    GOLDFLAGS="$GOLDFLAGS -X github.com/yggdrasil-network/yggdrasil-go/src/config.defaultAdminListen=unix:///var/run/yggdrasil/yggdrasil.sock"
    GOOS=linux GOARCH=$GOARCH GOARM=$GOARM CGO_ENABLED=0 just build ldflags="$GOLDFLAGS"
    mv yggdrasil yggdrasilctl "$OUTDIR/"

    PKGFILE="$ROOTDIR/build/{{pkgname}}-{{pkgversion}}-{{pkgarch}}.deb"
    PKGREPLACES="{{if pkgname == "yggdrasil" { "yggdrasil-develop" } else { "yggdrasil" }}}"
    BUILDDIR="$OUTDIR"

    mkdir -p $BUILDDIR/debian $BUILDDIR/usr/bin $BUILDDIR/lib/systemd/system

    cp $BUILDDIR/yggdrasil $BUILDDIR/yggdrasilctl $BUILDDIR/usr/bin/

    cat > $BUILDDIR/lib/systemd/system/yggdrasil.service << 'EOF'
    [Unit]
    Description=Yggdrasil Network
    Wants=network-online.target
    Wants=yggdrasil-default-config.service
    After=network-online.target
    After=yggdrasil-default-config.service

    [Service]
    Group=yggdrasil
    ProtectHome=true
    ProtectSystem=strict
    NoNewPrivileges=true
    RuntimeDirectory=yggdrasil
    ReadWritePaths=/var/run/yggdrasil/ /run/yggdrasil/
    ReadOnlyPaths=/etc/yggdrasil
    SyslogIdentifier=yggdrasil
    CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
    AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
    ExecStartPre=+-/sbin/modprobe tun
    ExecStart=/usr/bin/yggdrasil -useconffile /etc/yggdrasil/yggdrasil.conf
    ExecReload=/bin/kill -HUP $MAINPID
    Restart=always
    TimeoutStopSec=5

    [Install]
    WantedBy=multi-user.target
    EOF

    cat > $BUILDDIR/lib/systemd/system/yggdrasil-default-config.service << 'EOF'
    [Unit]
    Description=Yggdrasil default config generator
    ConditionPathExists=|!/etc/yggdrasil/yggdrasil.conf
    ConditionFileNotEmpty=|!/etc/yggdrasil/yggdrasil.conf
    Wants=local-fs.target
    After=local-fs.target

    [Service]
    Type=oneshot
    Group=yggdrasil
    UMask=037
    ExecStartPre=/usr/bin/mkdir -p /etc/yggdrasil
    ExecStart=/usr/bin/yggdrasil -genconf > /etc/yggdrasil/yggdrasil.conf
    ExecStartPost=/usr/bin/chmod -R 0640 /etc/yggdrasil
    EOF

    cat > $BUILDDIR/debian/changelog << 'EOF'
    Please see https://github.com/yggdrasil-network/yggdrasil-go/
    EOF

    echo 9 > $BUILDDIR/debian/compat

    cat > $BUILDDIR/debian/control << EOF
    Package: {{pkgname}}
    Version: {{pkgversion}}
    Section: golang
    Priority: optional
    Architecture: {{pkgarch}}
    Replaces: $PKGREPLACES
    Conflicts: $PKGREPLACES
    Depends: systemd
    Maintainer: Dmitry Moskowski <me@corpix.dev>
    Description: Yggdrasil Network (DPI-resistant fork)
     Fork of Yggdrasil focused on DPI resistance. Yggdrasil is a fully
     end-to-end encrypted IPv6 mesh network. It is lightweight, self-arranging,
     supported on multiple platforms and allows pretty much any IPv6-capable
     application to communicate securely with other Yggdrasil nodes.
    EOF

    cat > $BUILDDIR/debian/copyright << 'EOF'
    Please see https://github.com/yggdrasil-network/yggdrasil-go/
    EOF

    cat > $BUILDDIR/debian/docs << 'EOF'
    Please see https://github.com/yggdrasil-network/yggdrasil-go/
    EOF

    cat > $BUILDDIR/debian/install << 'EOF'
    usr/bin/yggdrasil usr/bin
    usr/bin/yggdrasilctl usr/bin
    lib/systemd/system/*.service lib/systemd/system
    EOF

    cat > $BUILDDIR/debian/postinst << 'EOF'
    #!/bin/sh
    systemctl daemon-reload
    if ! getent group yggdrasil 2>&1 > /dev/null; then
      groupadd --system --force yggdrasil
    fi
    if [ ! -d /etc/yggdrasil ]; then
      mkdir -p /etc/yggdrasil
      chown root:yggdrasil /etc/yggdrasil
      chmod 750 /etc/yggdrasil
    fi
    if [ ! -f /etc/yggdrasil/yggdrasil.conf ]; then
      test -f /etc/yggdrasil.conf && mv /etc/yggdrasil.conf /etc/yggdrasil/yggdrasil.conf
    fi
    if [ -f /etc/yggdrasil/yggdrasil.conf ]; then
      mkdir -p /var/backups
      cp /etc/yggdrasil/yggdrasil.conf "/var/backups/yggdrasil.conf.$(date +%Y%m%d)"
      /usr/bin/yggdrasil -useconf -normaliseconf < "/var/backups/yggdrasil.conf.$(date +%Y%m%d)" > /etc/yggdrasil/yggdrasil.conf
      chown root:yggdrasil /etc/yggdrasil/yggdrasil.conf
      chmod 640 /etc/yggdrasil/yggdrasil.conf
    else
      (umask 037 && /usr/bin/yggdrasil -genconf > /etc/yggdrasil/yggdrasil.conf)
      chown root:yggdrasil /etc/yggdrasil/yggdrasil.conf
      chmod 640 /etc/yggdrasil/yggdrasil.conf
    fi
    systemctl enable yggdrasil
    systemctl restart yggdrasil
    exit 0
    EOF

    cat > $BUILDDIR/debian/prerm << 'EOF'
    #!/bin/sh
    if command -v systemctl >/dev/null; then
      if systemctl is-active --quiet yggdrasil; then
        systemctl stop yggdrasil || true
      fi
      systemctl disable yggdrasil || true
    fi
    EOF

    tar --no-xattrs -czf $BUILDDIR/data.tar.gz -C $BUILDDIR \
      usr/bin/yggdrasil usr/bin/yggdrasilctl \
      lib/systemd/system/yggdrasil.service \
      lib/systemd/system/yggdrasil-default-config.service

    tar --no-xattrs -czf $BUILDDIR/control.tar.gz -C $BUILDDIR/debian .

    echo 2.0 > $BUILDDIR/debian-binary

    ar -r $PKGFILE \
      $BUILDDIR/debian-binary \
      $BUILDDIR/control.tar.gz \
      $BUILDDIR/data.tar.gz

build-macos pkgarch="amd64":
    #!/usr/bin/env bash
    set -eu -o pipefail

    ROOTDIR="$(pwd)"
    OUTDIR="$ROOTDIR/build/macos/{{pkgarch}}"
    mkdir -p "$OUTDIR"

    case "{{pkgarch}}" in
      amd64) GOARCH=amd64; PKGHOSTARCH=x86_64 ;;
      arm64) GOARCH=arm64; PKGHOSTARCH=arm64 ;;
      *) echo "Unknown pkgarch: {{pkgarch}}"; exit 1 ;;
    esac

    GOOS=darwin GOARCH=$GOARCH CGO_ENABLED=0 just build
    mv yggdrasil yggdrasilctl "$OUTDIR/"

    rm -rf "$OUTDIR/pkgbuild"
    mkdir -p "$OUTDIR/pkgbuild/scripts" \
             "$OUTDIR/pkgbuild/root/usr/local/bin" \
             "$OUTDIR/pkgbuild/root/Library/LaunchDaemons" \
             "$OUTDIR/pkgbuild/flat/base.pkg" \
             "$OUTDIR/pkgbuild/flat/Resources/en.lproj"

    cp "$OUTDIR/yggdrasil" "$OUTDIR/yggdrasilctl" "$OUTDIR/pkgbuild/root/usr/local/bin/"
    cat > "$OUTDIR/pkgbuild/root/Library/LaunchDaemons/yggdrasil.plist" << 'EOF'
    <?xml version="1.0" encoding="UTF-8"?>
    <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
    <plist version="1.0">
      <dict>
        <key>Label</key>
        <string>yggdrasil</string>
        <key>ProgramArguments</key>
        <array>
          <string>sh</string>
          <string>-c</string>
          <string>/usr/local/bin/yggdrasil -useconffile /etc/yggdrasil.conf</string>
        </array>
        <key>KeepAlive</key>
        <true/>
        <key>RunAtLoad</key>
        <true/>
        <key>ProcessType</key>
        <string>Interactive</string>
        <key>StandardOutPath</key>
        <string>/tmp/yggdrasil.stdout.log</string>
        <key>StandardErrorPath</key>
        <string>/tmp/yggdrasil.stderr.log</string>
      </dict>
    </plist>
    EOF
    chmod +x "$OUTDIR/pkgbuild/root/usr/local/bin/yggdrasil" \
             "$OUTDIR/pkgbuild/root/usr/local/bin/yggdrasilctl"

    cat > "$OUTDIR/pkgbuild/scripts/postinstall" << 'EOF'
    #!/bin/sh
    if [ -f /etc/yggdrasil.conf ]; then
      mkdir -p /Library/Preferences/Yggdrasil
      cp /etc/yggdrasil.conf "/Library/Preferences/Yggdrasil/yggdrasil.conf.$(date +%Y%m%d)"
      /usr/local/bin/yggdrasil -useconffile "/Library/Preferences/Yggdrasil/yggdrasil.conf.$(date +%Y%m%d)" -normaliseconf > /etc/yggdrasil.conf
    else
      (umask 037 && /usr/local/bin/yggdrasil -genconf > /etc/yggdrasil.conf)
    fi
    test -f /Library/LaunchDaemons/yggdrasil.plist && launchctl unload /Library/LaunchDaemons/yggdrasil.plist || true
    launchctl load /Library/LaunchDaemons/yggdrasil.plist
    EOF
    chmod +x "$OUTDIR/pkgbuild/scripts/postinstall"

    (cd "$OUTDIR/pkgbuild/scripts" && find . | cpio -o --format odc --owner 0:80 | gzip -c) > "$OUTDIR/pkgbuild/flat/base.pkg/Scripts"
    (cd "$OUTDIR/pkgbuild/root"    && find . | cpio -o --format odc --owner 0:80 | gzip -c) > "$OUTDIR/pkgbuild/flat/base.pkg/Payload"

    PAYLOADSIZE=$(( $(wc -c < "$OUTDIR/pkgbuild/flat/base.pkg/Payload") / 1024 ))

    cat > "$OUTDIR/pkgbuild/flat/base.pkg/PackageInfo" << EOF
    <pkg-info format-version="2" identifier="io.github.yggdrasil-network.pkg" version="{{pkgversion}}" install-location="/" auth="root">
      <payload installKBytes="${PAYLOADSIZE}" numberOfFiles="3"/>
      <scripts><postinstall file="./postinstall"/></scripts>
    </pkg-info>
    EOF

    (cd "$OUTDIR/pkgbuild" && mkbom root flat/base.pkg/Bom)

    cat > "$OUTDIR/pkgbuild/flat/Distribution" << EOF
    <?xml version="1.0" encoding="utf-8"?>
    <installer-script minSpecVersion="1.000000">
      <title>Yggdrasil ({{pkgname}}-{{pkgversion}})</title>
      <options customize="never" allow-external-scripts="no" hostArchitectures="${PKGHOSTARCH}"/>
      <domains enable_anywhere="true"/>
      <choices-outline><line choice="choice1"/></choices-outline>
      <choice id="choice1" title="base"><pkg-ref id="io.github.yggdrasil-network.pkg"/></choice>
      <pkg-ref id="io.github.yggdrasil-network.pkg" installKBytes="${PAYLOADSIZE}" version="{{pkgversion}}" auth="Root">#base.pkg</pkg-ref>
    </installer-script>
    EOF

    (cd "$OUTDIR/pkgbuild/flat" && xar --compression none -cf "$ROOTDIR/build/{{pkgname}}-{{pkgversion}}-macos-{{pkgarch}}.pkg" *)

build-windows pkgarch="x64":
    #!/usr/bin/env bash
    set -eu -o pipefail

    ROOTDIR="$(pwd)"
    OUTDIR="$ROOTDIR/build/windows/{{pkgarch}}"
    mkdir -p "$OUTDIR"

    case "{{pkgarch}}" in
      x64)   GOARCH=amd64; PKGGUID="77757838-1a23-40a5-a720-c3b43e0260cc"; PKGINSTFOLDER="ProgramFiles64Folder" ;;
      x86)   GOARCH=386;   PKGGUID="54a3294e-a441-4322-aefb-3bb40dd022bb"; PKGINSTFOLDER="ProgramFilesFolder" ;;
      arm64) GOARCH=arm64; PKGGUID="77757838-1a23-40a5-a720-c3b43e0260cc"; PKGINSTFOLDER="ProgramFiles64Folder" ;;
      *) echo "Unknown pkgarch: {{pkgarch}}"; exit 1 ;;
    esac

    GOOS=windows GOARCH=$GOARCH CGO_ENABLED=0 just build
    mv yggdrasil.exe yggdrasilctl.exe "$OUTDIR/"
    cat > "$OUTDIR/README.txt" << 'EOF'
    Yggdrasil Windows service

    The installer registers the Windows service named:
      Yggdrasil

    Manage it from an elevated Command Prompt:
      sc query Yggdrasil
      sc start Yggdrasil
      sc stop Yggdrasil

    Or from an elevated PowerShell:
      Get-Service Yggdrasil
      Restart-Service Yggdrasil

    Installed binaries:
      %ProgramFiles%\Yggdrasil

    Config and logs:
      %ProgramData%\Yggdrasil
      %ProgramData%\Yggdrasil\yggdrasil.conf
      %ProgramData%\Yggdrasil\yggdrasil.log
    EOF

    # wixl requires N.N.N.N version; use commit count as fourth component
    BASE=$(echo "{{pkgversion}}" | grep -oE '^[0-9]+\.[0-9]+\.[0-9]+' || echo "0.0.0")
    BUILD=$(echo "{{pkgversion}}" | grep -oE -- '-[0-9]+$' | tr -d '-' || echo "0")
    PKGVERSIONMS="${BASE}.${BUILD:-0}"

    if [ ! -d "$OUTDIR/wintun" ]; then
      curl -o "$OUTDIR/wintun.zip" https://www.wintun.net/builds/wintun-0.14.1.zip
      if [ "$(sha256sum "$OUTDIR/wintun.zip" | cut -f1 -d' ')" != "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51" ]; then
        echo "wintun checksum mismatch"; exit 1
      fi
      python3 -m zipfile -e "$OUTDIR/wintun.zip" "$OUTDIR"
    fi
    case "{{pkgarch}}" in
      x64)   PKGWINTUNDLL="$OUTDIR/wintun/bin/amd64/wintun.dll" ;;
      x86)   PKGWINTUNDLL="$OUTDIR/wintun/bin/x86/wintun.dll" ;;
      arm64) PKGWINTUNDLL="$OUTDIR/wintun/bin/arm64/wintun.dll" ;;
    esac

    cat > "$OUTDIR/package.wxs" << EOF
    <?xml version="1.0" encoding="utf-8"?>
    <Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
      <Product
        Name="{{pkgdisplayname}}"
        Version="${PKGVERSIONMS}"
        Manufacturer="github.com/yggdrasil-network"
        UpgradeCode="${PKGGUID}"
        Language="1033"
        Codepage="1252"
        Id="*">

        <Package InstallerVersion="500" Compressed="yes" InstallScope="perMachine" />

        <MajorUpgrade AllowDowngrades="yes" />
        <Media Id="1" Cabinet="product.cab" EmbedCab="yes" />

        <Directory Id="TARGETDIR" Name="SourceDir">
          <Directory Id="${PKGINSTFOLDER}">
            <Directory Id="Yggdrasil" Name="Yggdrasil">
              <Component Id="MainExecutable" Guid="c2119231-2aa3-4962-867a-9759c87beb24">
                <File Id="Yggdrasil" Name="yggdrasil.exe" Source="${OUTDIR}/yggdrasil.exe" KeyPath="yes" />
                <File Id="Wintun" Name="wintun.dll" Source="${PKGWINTUNDLL}" />
                <Environment
                  Id="AddYggdrasilToPath"
                  Action="set"
                  Name="PATH"
                  Part="last"
                  System="yes"
                  Value="[Yggdrasil]" />
                <ServiceInstall
                  Id="ServiceInstaller"
                  Account="LocalSystem"
                  Description="Yggdrasil Network router process"
                  DisplayName="Yggdrasil Service"
                  ErrorControl="normal"
                  LoadOrderGroup="NetworkProvider"
                  Name="Yggdrasil"
                  Start="auto"
                  Type="ownProcess"
                  Arguments='-useconffile "%ALLUSERSPROFILE%\Yggdrasil\yggdrasil.conf" -logto "%ALLUSERSPROFILE%\Yggdrasil\yggdrasil.log"'
                  Vital="yes" />
                <ServiceControl Id="ServiceControl" Name="Yggdrasil" Start="install" Stop="both" Remove="uninstall" />
              </Component>
              <Component Id="CtrlExecutable" Guid="a916b730-974d-42a1-b687-d9d504cbb86a">
                <File Id="Yggdrasilctl" Name="yggdrasilctl.exe" Source="${OUTDIR}/yggdrasilctl.exe" KeyPath="yes" />
              </Component>
              <Component Id="ServiceReadme" Guid="64a3733b-c98a-4732-85f3-20cd7da1a785">
                <File Id="ServiceReadmeTxt" Name="README.txt" Source="${OUTDIR}/README.txt" KeyPath="yes" />
              </Component>
            </Directory>
          </Directory>
          <Directory Id="CommonAppDataFolder">
            <Directory Id="YggdrasilData" Name="Yggdrasil">
              <Component Id="DataDir" Guid="b3e5f6a2-1c4d-4e8f-9a2b-3d7e8f1a2b4c">
                <CreateFolder />
              </Component>
            </Directory>
          </Directory>
        </Directory>

        <Feature Id="YggdrasilFeature" Title="Yggdrasil" Level="1">
          <ComponentRef Id="MainExecutable" />
          <ComponentRef Id="CtrlExecutable" />
          <ComponentRef Id="ServiceReadme" />
          <ComponentRef Id="DataDir" />
        </Feature>

        <Property Id="YGG_ENABLE_WEBUI" Value="0" />

        <CustomAction Id="ForceReinstallAll" Property="REINSTALL" Value="ALL" />
        <CustomAction Id="ForceReinstallMode" Property="REINSTALLMODE" Value="amus" />
        <CustomAction
          Id="UpdateGenerateConfig"
          FileKey="Yggdrasil"
          ExeCommand='-useconffile "[YggdrasilData]yggdrasil.conf" -normaliseconf'
          Execute="immediate"
          Return="check"
          Impersonate="no" />

        <InstallExecuteSequence>
          <Custom Action="ForceReinstallAll" Before="CostInitialize">Installed AND NOT REMOVE</Custom>
          <Custom Action="ForceReinstallMode" Before="CostInitialize">Installed AND NOT REMOVE</Custom>
          <Custom Action="UpdateGenerateConfig" After="InstallFiles">NOT Installed AND NOT REMOVE</Custom>
        </InstallExecuteSequence>

      </Product>

      <Fragment>
        <UI>
          <TextStyle Id="WixUI_Font_Normal" FaceName="Tahoma" Size="8" />
          <TextStyle Id="WixUI_Font_Bigger" FaceName="Tahoma" Size="12" />
          <TextStyle Id="WixUI_Font_Title" FaceName="Tahoma" Size="9" Bold="yes" />
          <Property Id="DefaultUIFont" Value="WixUI_Font_Normal" />
          <Dialog Id="ExitDialog" Width="370" Height="270" Title="[ProductName] Setup">
            <Control Id="Finish" Type="PushButton" X="236" Y="243" Width="56" Height="17" Default="yes" Cancel="yes" Text="Finish" />
            <Control Id="Cancel" Type="PushButton" X="304" Y="243" Width="56" Height="17" Disabled="yes" Text="Cancel" />
            <Control Id="Bitmap" Type="Bitmap" X="0" Y="0" Width="370" Height="234" TabSkip="no" Text="WixUI_Bmp_Dialog" />
            <Control Id="Back" Type="PushButton" X="180" Y="243" Width="56" Height="17" Disabled="yes" Text="Back" />
            <Control Id="BottomLine" Type="Line" X="0" Y="234" Width="370" Height="0" />
            <Control Id="Description" Type="Text" X="135" Y="70" Width="220" Height="28" Transparent="yes" NoPrefix="yes" Text="Yggdrasil is installed and set to start automatically at boot." />
            <Control Id="ReminderText" Type="Text" X="135" Y="104" Width="220" Height="98" Transparent="yes" NoPrefix="yes" Text="Manage service from Command Prompt:&#10;sc query Yggdrasil&#10;sc start Yggdrasil&#10;sc stop Yggdrasil&#10;&#10;Files: [Yggdrasil]&#10;Data: [YggdrasilData]&#10;More commands: [Yggdrasil]README.txt" />
            <Control Id="Title" Type="Text" X="135" Y="20" Width="220" Height="40" Transparent="yes" NoPrefix="yes" Text="{\WixUI_Font_Bigger}Completed the [ProductName] Setup Wizard" />
          </Dialog>
          <InstallUISequence>
            <Show Dialog="ExitDialog" OnExit="success" Overridable="yes" />
          </InstallUISequence>
          <AdminUISequence>
            <Show Dialog="ExitDialog" OnExit="success" Overridable="yes" />
          </AdminUISequence>
          <Publish Dialog="ExitDialog" Control="Finish" Event="EndDialog" Value="Return" Order="999" />
        </UI>
        <UIRef Id="WixUI_Common" />
      </Fragment>
    </Wix>
    EOF

    # debugging:
    # msiexec /i yggdrasil-corpix-0.5.13-29-x64.msi /l*v C:\install.log
    # sc start yggdrasil
    # sc query yggdrasil
    wixl --ext ui -a "{{pkgarch}}" -o "$ROOTDIR/build/{{pkgname}}-{{pkgversion}}-{{pkgarch}}.msi" "$OUTDIR/package.wxs"

lint:
  golangci-lint run -v

fmt:
  gofumpt -w ./
  goimports -format-only -local github.com/yggdrasil-network/ -w ./src ./cmd ./contrib

# Destructive clean
clean:
  git clean -dxf
