{
  inputs = {
    automata.url = "github:shikanime-studio/automata";
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
        { pkgs, ... }:
        {
          devenv.shells.default = {
            imports = [
              devlib.devenvModules.go
              devlib.devenvModules.nix
              devlib.devenvModules.shell
              devlib.devenvModules.shikanime-studio
            ];

            github.settings.workflows = {
              integration.jobs.build = {
                permissions.packages = "write";
                "runs-on" = "ubuntu-latest";
                steps = [
                  {
                    id = "createGithubAppToken";
                    uses = "actions/create-github-app-token@v1";
                    "with" = {
                      app-id = "\${{ vars.OPERATOR_APP_ID }}";
                      private-key = "\${{ secrets.OPERATOR_PRIVATE_KEY }}";
                      permission-contents = "read";
                    };
                  }
                  {
                    uses = "actions/checkout@v4";
                    "with".token = "\${{ steps.createGithubAppToken.outputs.token || secrets.GITHUB_TOKEN }}";
                  }
                  {
                    uses = "cachix/install-nix-action@v30";
                    "with".github_access_token =
                      "\${{ steps.createGithubAppToken.outputs.token || secrets.GITHUB_TOKEN }}";
                  }
                  {
                    uses = "docker/login-action@v3";
                    "with" = {
                      registry = "ghcr.io";
                      username = "\${{ github.actor }}";
                      password = "\${{ secrets.GITHUB_TOKEN }}";
                    };
                  }
                  {
                    env = { };
                    run = "nix run nixpkgs#direnv allow";
                  }
                  {
                    env = { };
                    run = "nix run nixpkgs#direnv export gha >> \"$GITHUB_ENV\"";
                  }
                  {
                    run = "skaffold --command skaffold build --platform linux/amd64,linux/arm64";
                  }
                ];
              };

              release.jobs = {
                build = {
                  permissions.packages = "write";
                  "runs-on" = "ubuntu-latest";
                  steps = [
                    {
                      id = "createGithubAppToken";
                      uses = "actions/create-github-app-token@v1";
                      "with" = {
                        app-id = "\${{ vars.OPERATOR_APP_ID }}";
                        private-key = "\${{ secrets.OPERATOR_PRIVATE_KEY }}";
                        permission-contents = "write";
                      };
                    }
                    {
                      uses = "actions/checkout@v4";
                      "with".token = "\${{ steps.createGithubAppToken.outputs.token || secrets.GITHUB_TOKEN }}";
                    }
                    {
                      uses = "cachix/install-nix-action@v30";
                      "with".github_access_token =
                        "\${{ steps.createGithubAppToken.outputs.token || secrets.GITHUB_TOKEN }}";
                    }
                    {
                      uses = "docker/login-action@v3";
                      "with" = {
                        registry = "ghcr.io";
                        username = "\${{ github.actor }}";
                        password = "\${{ secrets.GITHUB_TOKEN }}";
                      };
                    }
                    {
                      env = { };
                      run = "nix run nixpkgs#direnv allow";
                    }
                    {
                      env = { };
                      run = "nix run nixpkgs#direnv export gha >> \"$GITHUB_ENV\"";
                    }
                    {
                      run = "skaffold --command skaffold render --output tailscale-gateway.yaml";
                    }
                    {
                      uses = "actions/upload-artifact@v5";
                      "with" = {
                        name = "deploy";
                        path = "tailscale-gateway.yaml";
                      };
                    }
                  ];
                };

                upload = {
                  permissions.packages = "write";
                  needs = [
                    "build"
                    "release-tag"
                  ];
                  "runs-on" = "ubuntu-latest";
                  steps = [
                    {
                      id = "createGithubAppToken";
                      uses = "actions/create-github-app-token@v1";
                      "with" = {
                        app-id = "\${{ vars.OPERATOR_APP_ID }}";
                        private-key = "\${{ secrets.OPERATOR_PRIVATE_KEY }}";
                        permission-contents = "write";
                      };
                    }
                    {
                      uses = "actions/checkout@v4";
                      "with".token = "\${{ steps.createGithubAppToken.outputs.token || secrets.GITHUB_TOKEN }}";
                    }
                    {
                      uses = "actions/download-artifact@v4";
                      "with".name = "deploy";
                    }
                    {
                      env.GITHUB_TOKEN = "\${{ steps.createGithubAppToken.outputs.token || secrets.GITHUB_TOKEN }}";
                      run = "gh release upload \"\${{ github.ref_name }}\" --repo \"\${{ github.repository }}\" tailscale-gateway.yaml";
                    }
                  ];
                };
              };
            };

            packages = [
              pkgs.ko
              pkgs.kubectl
              pkgs.skaffold
            ];

            sops = {
              enable = true;
              settings.creation_rules = [
                {
                  key_groups = [
                    {
                      age = [
                        "age139fcg32lmhxupnz5wjex44jur7v7wzf9rttp2grnjmxhukck5dmqsd9zj5" # kaltashar
                        "age1pwl9yz4k4255a4h8qz7lafce8wxhsul0cnqwmr8528fqgujlfshshv3z3g" # telsha
                        "age1x9v4ps90txy9mk4392uya93tyzx40te4dvns4chg5s6q8mfy03ns74jpay" # nixtar
                      ];
                    }
                  ];
                }
              ];
            };
          };

          packages.default = pkgs.buildGoModule {
            pname = "tailscale-gateway";
            version = "v0.1.0";
            src = pkgs.lib.cleanSource ./.;
            subPackages = [ "cmd/tailscale-gateway-controller" ];
            vendorHash = pkgs.lib.fakeHash;
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
