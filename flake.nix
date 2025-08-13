{
  description =
    "gosmee — command line server and client for webhooks deliveries (and https://smee.io)";

  inputs.utils.url = "github:numtide/flake-utils";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs";

  outputs = { self, nixpkgs, utils }:
    let
      # Generate a user-friendly version number.
      version = self.rev or (builtins.substring 0 8 self.lastModifiedDate);
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      nixosConfigurations.gosmee-test = nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";
        modules = [
          ./nix/test-configuration.nix
          (import "${nixpkgs}/nixos/modules/virtualisation/docker-image.nix")
          ({ pkgs, ... }: {
            nixpkgs.overlays = [ self.overlays.default ];
          })
        ];
      };
      overlays.default = final: prev: {
        gosmee =
          (import ./default.nix { inherit (final) buildGo124Module; }) {
            packageSrc = self;
            version = version;
          };
      };
    } // (utils.lib.eachSystem systems (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in {
        packages = {
          gosmee = self.packages.${system}.gosmee;
          default = self.packages.${system}.gosmee;
        };
        checks = {
          gosmee-module-eval =
            let
              _ = nixpkgs.lib.evalModules {
                modules = [
                  (import ./nix/nixos-module.nix)
                  ({ ... }: {
                    services.gosmee.server = {
                      enable = true;
                      address = "127.0.0.1";
                      port = 3333;
                      trustProxy = false;
                    };
                    services.gosmee.clients.example = {
                      enable = true;
                      smeeUrl = "https://smee.io/example";
                      targetUrl = "http://127.0.0.1:8080";
                      targetConnectionTimeout = 5;
                    };
                  })
                ];
                specialArgs = { inherit pkgs; lib = nixpkgs.lib; };
              };
            in pkgs.writeText "gosmee-module-eval" "ok";
        };
        nixosModules = {
          default = import ./nix/nixos-module.nix;
          gosmee = import ./nix/nixos-module.nix;
        };
        apps = {
          gosmee = utils.lib.mkApp {
            drv = self.packages.${system}.gosmee;
            name = "gosmee";
          };
          default = self.apps.${system}.gosmee;
        };
        devShell = pkgs.mkShell {
          nativeBuildInputs = [
            pkgs.go_1_24
            pkgs.gnumake
            pkgs.pre-commit # needed for pre-commit install
            pkgs.git # needed for pre-commit install
            pkgs.yamllint # needed for pre-commit install
          ];
          shellHook = ''
            pre-commit install
          '';
        };
      }));
}
