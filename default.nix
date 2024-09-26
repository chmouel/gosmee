{ stdenv, lib, buildGoModule, version, packageSrc ? ./. }:

buildGoModule rec {
  name = "gosmee-${version}";

  src = packageSrc;
  vendorHash = null;

  postUnpack = ''
    printf ${version} > $sourceRoot/gosmee/templates/version
  '';

  postInstall = ''
    # completions
    mkdir -p $out/share/bash-completion/completions/
    $out/bin/gosmee completion bash > $out/share/bash-completion/completions/gosmee
    mkdir -p $out/share/zsh/site-functions
    $out/bin/gosmee completion zsh > $out/share/zsh/site-functions/_gosmee
  '';

  meta = {
    description =
      "Command line server and client for webhooks deliveries (and https://smee.io)";
    homepage = "https://github.com/chmouel/gosmee";
    license = lib.licenses.asl20;
  };
}

