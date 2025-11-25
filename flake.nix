{
  inputs = {
    devenv.url = "github:cachix/devenv";
    devlib.url = "github:shikanime-studio/devlib";
    flake-parts.url = "github:hercules-ci/flake-parts";
    git-hooks.url = "github:cachix/git-hooks.nix";
    nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
    treefmt-nix.url = "github:numtide/treefmt-nix";
  };

  nixConfig = {
    extra-substituters = [
      "https://cachix.cachix.org"
      "https://devenv.cachix.org"
      "https://shikanime.cachix.org"
      "https://shikanime-studio.cachix.org"
    ];
    extra-trusted-public-keys = [
      "cachix.cachix.org-1:eWNHQldwUO7G2VkjpnjDbWwy4KQ/HNxht7H4SSoMckM="
      "devenv.cachix.org-1:w1cLUi8dv3hnoSPGAuibQv+f9TZLr6cv/Hm9XgU50cw="
      "shikanime.cachix.org-1:OrpjVTH6RzYf2R97IqcTWdLRejF6+XbpFNNZJxKG8Ts="
      "shikanime-studio.cachix.org-1:KxV6aDFU81wzoR9u6pF1uq0dQbUuKbodOSP8/EJHXO0="
    ];
  };

  outputs =
    inputs@{
      devenv,
      devlib,
      flake-parts,
      git-hooks,
      treefmt-nix,
      ...
    }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      imports = [
        devenv.flakeModule
        devlib.flakeModule
        git-hooks.flakeModule
        treefmt-nix.flakeModule
      ];
      perSystem =
        { config, pkgs, ... }:
        {
          devenv.shells.default = {
            imports = [
              devlib.devenvModules.shikanime-studio
            ];
            github = {
              actions = with config.devenv.shells.default.github.lib; {
                download-artifacts = {
                  uses = "actions/download-artifact@v4";
                  "with".name = "manifests";
                };

                skaffold-build = {
                  run = mkWorkflowRun [
                    "nix"
                    "shell"
                    "nixpkgs#ko"
                    "nixpkgs#skaffold"
                    "--command"
                    "skaffold"
                    "build"
                    "--platform"
                    "linux/amd64,linux/arm64"
                  ];
                };

                skaffold-render = {
                  run = mkWorkflowRun [
                    "nix"
                    "shell"
                    "nixpkgs#ko"
                    "nixpkgs#skaffold"
                    "--command"
                    "skaffold"
                    "render"
                    "--output"
                    "manifests.yaml"
                  ];
                };

                upload-artifacts = {
                  uses = "actions/upload-artifact@v4";
                  "with" = {
                    name = "manifests";
                    path = "manifests.yaml";
                  };
                };

                release-upload-manifests = {
                  run = mkWorkflowRun [
                    "gh"
                    "release"
                    "upload"
                    (mkWorkflowRef "github.ref_name")
                    "--repo"
                    (mkWorkflowRef "github.repository")
                    "manifests/manifests.yaml"
                  ];
                };
              };
              workflows = {
                main.settings.jobs = {
                  build = {
                    needs = [ "check" ];
                    permissions.packages = "write";
                    "runs-on" = "ubuntu-latest";
                    steps = with config.devenv.shells.default.github.actions; [
                      create-github-app-token
                      checkout
                      setup-nix
                      docker-login
                      skaffold-build
                      skaffold-render
                      upload-artifacts
                    ];
                  };
                };
                release.settings.jobs = {
                  build = {
                    needs = [ "check" ];
                    "runs-on" = "ubuntu-latest";
                    steps = with config.devenv.shells.default.github.actions; [
                      create-github-app-token
                      checkout
                      setup-nix
                      docker-login
                      skaffold-build
                      skaffold-render
                      upload-artifacts
                    ];
                  };
                  upload = {
                    needs = [
                      "build"
                      "publish"
                    ];
                    permissions.packages = "write";
                    "runs-on" = "ubuntu-latest";
                    steps = with config.devenv.shells.default.github.actions; [
                      create-github-app-token
                      checkout
                      download-artifacts
                      release-upload-manifests
                    ];
                  };
                };
              };
            };
            languages.go.enable = true;
            packages = [
              pkgs.ko
              pkgs.kubectl
              pkgs.skaffold
              pkgs.sops
            ];
          };

          packages.default = pkgs.buildGoModule {
            pname = "tailscale-gateway";
            version = "v0.1.0";
            src = pkgs.lib.cleanSource ./.;
            subPackages = [ "cmd/tailscale-gateway-controller" ];
            vendorHash = "sha256-TrzjYQJMZxW907rpoRMmtJgTIbUC/OMNJzXXlQjcPb4=";
            meta = with pkgs.lib; {
              description = "Tailscale Gateway";
              homepage = "https://github.com/shikanime-studio/tailscale-gateway";
              license = licenses.asl20;
              mainProgram = "tailscale-gateway-controller";
            };
          };
        };
      systems = [
        "x86_64-linux"
        "x86_64-darwin"
        "aarch64-linux"
        "aarch64-darwin"
      ];
    };
}
