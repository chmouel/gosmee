{
  description = "gosmee — command line server and client for webhooks deliveries (and https://smee.io)";

  inputs.utils.url = "github:numtide/flake-utils";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs";

  outputs = { self, nixpkgs, utils }:
    let
      # Generate a user-friendly version number.
      version = self.rev or (builtins.substring 0 8 self.lastModifiedDate);
    in
    utils.lib.eachSystem [ "x86_64-linux" "aarch64-linux" ] (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        gosmee = pkgs.callPackage ./default.nix {
          packageSrc = self;
          buildGoModule = pkgs.buildGo119Module;
          version = version;
        };
      in
      {
        packages = {
          gosmee = gosmee;
          default = self.packages.${system}.gosmee;
        };
        apps = {
          gosmee = utils.lib.mkApp { drv = self.packages.${system}.gosmee; name = "gosmee"; };
          default = self.apps.${system}.gosmee;
        };
        overlay = final: prev: {
          gosmee = gosmee;
        };
        devShell = pkgs.mkShell
          {
            nativeBuildInputs = [
              pkgs.go_1_19
              pkgs.gnumake
              pkgs.pre-commit # needed for pre-commit install
              pkgs.git # needed for pre-commit install
              pkgs.yamllint # needed for pre-commit install
            ];
            shellHook = ''
              pre-commit install
            '';
          };
      });
}
