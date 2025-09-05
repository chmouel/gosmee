{ config, pkgs, ... }:

{
  # Import your module so we can use its options
  imports = [
    ./nixos-module.nix
  ];

  # This is a special setting to optimize the build for a container
  boot.isContainer = true;

  # Enable the gosmee server
  services.gosmee.server = {
    enable = true;
    # Open the firewall so the client can connect
    openFirewall = true;
  };

  # Enable a test client instance
  services.gosmee.clients."test-client" = {
    enable = true;
    # The smeeUrl points to the server in this same container
    smeeUrl = "http://localhost:3333/test-channel";
    # The targetUrl is where the client will forward requests
    targetUrl = "http://localhost:8080";
  };

  # We need curl and netcat for testing
  environment.systemPackages = [ pkgs.curl pkgs.netcat ];

  # Enable and configure the firewall
  networking.firewall.enable = true;
}
