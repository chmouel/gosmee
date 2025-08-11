{ lib, pkgs, config, ... }:

let
  inherit (lib) mkOption mkEnableOption mkIf mkMerge types optionalString concatStringsSep;
  cfg = config.services.gosmee;
in
{
  options.services.gosmee = {
    package = mkOption {
      type = types.package;
      default = pkgs.gosmee;
      description = "Package providing the `gosmee` binary.";
    };

    server = {
      enable = mkEnableOption "gosmee relay server";

      address = mkOption {
        type = types.str;
        default = "localhost";
        description = "Address to listen on.";
      };

      port = mkOption {
        type = types.port;
        default = 3333;
        description = "Port to listen on.";
      };

      publicUrl = mkOption {
        type = types.nullOr types.str;
        default = null;
        description = "Public URL (shown in UI and new URL generation). If null, derived from address:port and TLS flags.";
      };

      allowedIPs = mkOption {
        type = types.listOf types.str;
        default = [];
        description = "List of allowed IPs/CIDRs for POST requests (empty allows all).";
      };

      trustProxy = mkOption {
        type = types.bool;
        default = false;
        description = "Trust X-Forwarded-For and X-Real-IP headers when behind a proxy.";
      };

      webhookSignatures = mkOption {
        type = types.listOf types.str;
        default = [];
        description = "Secrets to validate webhook signatures (repeatable).";
      };

      tlsCert = mkOption {
        type = types.nullOr types.path;
        default = null;
        description = "Path to TLS certificate file (enables TLS when set with tlsKey).";
      };

      tlsKey = mkOption {
        type = types.nullOr types.path;
        default = null;
        description = "Path to TLS key file (enables TLS when set with tlsCert).";
      };

      autoCert = mkOption {
        type = types.bool;
        default = false;
        description = "Use Let's Encrypt autocert with the provided public URL host.";
      };

      footer = mkOption {
        type = types.nullOr types.str;
        default = null;
        description = "HTML footer string to render in UI.";
      };

      footerFile = mkOption {
        type = types.nullOr types.path;
        default = null;
        description = "Path to HTML footer file to render in UI.";
      };

      openFirewall = mkOption {
        type = types.bool;
        default = false;
        description = "Open the configured TCP port in the firewall.";
      };
    };

    # Multiple client instances
    clients = mkOption {
      type = types.attrsOf (types.submodule ({ name, ... }: {
        options = {
          enable = mkEnableOption ("gosmee client instance " + name);

          smeeUrl = mkOption {
            type = types.str;
            description = "Remote gosmee/smee URL to subscribe to (e.g., https://smee.io/abcd or https://your.server/channel).";
          };

          targetUrl = mkOption {
            type = types.str;
            description = "Local/private target service URL (e.g., http://127.0.0.1:8080).";
          };

          channel = mkOption {
            type = types.str;
            default = "messages";
            description = "Channel to listen to (used for self-hosted server URLs).";
          };

          saveDir = mkOption {
            type = types.nullOr types.str;
            default = null;
            description = "Directory to save replay scripts and payloads. Defaults to /var/lib/gosmee/clients/<name> when null.";
          };

          noReplay = mkOption {
            type = types.bool;
            default = false;
            description = "Do not forward to target; only save if saveDir is set.";
          };

          ignoreEvents = mkOption {
            type = types.listOf types.str;
            default = [];
            description = "Event types to ignore (e.g., push).";
          };

          insecureSkipTLSVerify = mkOption {
            type = types.bool;
            default = false;
            description = "Skip TLS verification when forwarding to target.";
          };

          httpie = mkOption {
            type = types.bool;
            default = false;
            description = "Generate HTTPie replay scripts instead of cURL (requires httpie).";
          };

          output = mkOption {
            type = types.enum [ "json" "pretty" ];
            default = "pretty";
            description = "Client log output format.";
          };

          targetConnectionTimeout = mkOption {
            type = types.ints.positive;
            default = 5;
            description = "Timeout in seconds for forwarding requests to target.";
          };

          healthPort = mkOption {
            type = types.nullOr types.port;
            default = null;
            description = "Optional port to expose a health endpoint.";
          };
        };
      }));
      default = {};
      description = "Attribute set of gosmee client instances.";
    };
  };

  config = mkMerge [
    (mkIf cfg.server.enable {
      users.groups.gosmee = { };
      users.users.gosmee = {
        isSystemUser = true;
        group = "gosmee";
      };

      networking.firewall.allowedTCPPorts = mkIf cfg.server.openFirewall [ cfg.server.port ];

      systemd.services.gosmee-server = {
        description = "gosmee relay server";
        wantedBy = [ "multi-user.target" ];
        after = [ "network-online.target" ];
        wants = [ "network-online.target" ];
        serviceConfig = {
          User = "gosmee";
          Group = "gosmee";
          Restart = "on-failure";
          RestartSec = 2;
          # Keep a state dir for potential future persistence needs
          StateDirectory = "gosmee";
          AmbientCapabilities = [ "CAP_NET_BIND_SERVICE" ];
          NoNewPrivileges = true;
          LockPersonality = true;
          ProtectSystem = "strict";
          ProtectHome = true;
          PrivateTmp = true;
          PrivateDevices = true;
          ProtectKernelTunables = true;
          ProtectKernelModules = true;
          ProtectControlGroups = true;
          RestrictSUIDSGID = true;
          RestrictRealtime = true;
          SystemCallFilter = [ "@system-service" ];
          ExecStart = let
            args = [
              "server"
              "--address" cfg.server.address
              "--port" (toString cfg.server.port)
            ]
            ++ (lib.optionals (cfg.server.publicUrl != null) [ "--public-url" cfg.server.publicUrl ])
            ++ (lib.concatMap (ip: [ "--allowed-ips" ip ]) cfg.server.allowedIPs)
            ++ (lib.optionals cfg.server.trustProxy [ "--trust-proxy" ])
            ++ (lib.concatMap (s: [ "--webhook-signature" s ]) cfg.server.webhookSignatures)
            ++ (lib.optionals (cfg.server.tlsCert != null && cfg.server.tlsKey != null) [
              "--tls-cert" (toString cfg.server.tlsCert)
              "--tls-key" (toString cfg.server.tlsKey)
            ])
            ++ (lib.optionals cfg.server.autoCert [ "--auto-cert" ])
            ++ (lib.optionals (cfg.server.footer != null && cfg.server.footerFile == null) [
              "--footer" cfg.server.footer
            ])
            ++ (lib.optionals (cfg.server.footerFile != null && cfg.server.footer == null) [
              "--footer-file" (toString cfg.server.footerFile)
            ]);
          in
            lib.escapeShellArgs ([ "${cfg.package}/bin/gosmee" ] ++ args);
        };
      };
    })

    # Clients
    (mkIf (cfg.clients != {}) {
      users.groups.gosmee = { };
      users.users.gosmee = {
        isSystemUser = true;
        group = "gosmee";
      };

      systemd.services = lib.mapAttrs' (name: icfg:
        let
          saveDirDefault = "/var/lib/gosmee/clients/${name}";
          saveDir = icfg.saveDir or saveDirDefault;
          stateDir = lib.optionalString (saveDir == saveDirDefault) "gosmee/clients/${name}";
          args = [
            "client"
            "--output" icfg.output
            "--channel" icfg.channel
            "--target-connection-timeout" (toString icfg.targetConnectionTimeout)
          ]
          ++ (lib.concatMap (ev: [ "--ignore-event" ev ]) icfg.ignoreEvents)
          ++ (lib.optionals icfg.noReplay [ "--noReplay" ])
          ++ (lib.optionals icfg.httpie [ "--httpie" ])
          ++ (lib.optionals icfg.insecureSkipTLSVerify [ "--insecure-skip-tls-verify" ])
          ++ (lib.optionals (icfg.healthPort != null) [ "--health-port" (toString icfg.healthPort) ])
          ++ (lib.optionals (saveDir != null) [ "--saveDir" saveDir ])
          ++ [ icfg.smeeUrl icfg.targetUrl ];
        in
          lib.nameValuePair "gosmee-client-${name}" {
            description = "gosmee client ${name}";
            wantedBy = [ "multi-user.target" ];
            after = [ "network-online.target" ];
            wants = [ "network-online.target" ];
            serviceConfig = {
              User = "gosmee";
              Group = "gosmee";
              Restart = "always";
              RestartSec = 2;
              StateDirectory = lib.mkIf (stateDir != "") stateDir;
              NoNewPrivileges = true;
              LockPersonality = true;
              ProtectSystem = "strict";
              ProtectHome = true;
              PrivateTmp = true;
              PrivateDevices = true;
              ProtectKernelTunables = true;
              ProtectKernelModules = true;
              ProtectControlGroups = true;
              RestrictSUIDSGID = true;
              RestrictRealtime = true;
              SystemCallFilter = [ "@system-service" ];
              ExecStart = lib.escapeShellArgs ([ "${cfg.package}/bin/gosmee" ] ++ args);
            };
            enable = icfg.enable;
          }
      ) cfg.clients;
    })
  ];
}

