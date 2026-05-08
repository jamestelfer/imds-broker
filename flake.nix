{
  description = "Vends AWS credentials via IMDSv2 for Docker containers, local dev tools, and AI agents";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        packages = {
          imds-broker = pkgs.buildGoModule {
            pname = "imds-broker";
            version = "0.3.0";

            src = ./.;

            vendorHash = "sha256-goNU950kwkBVSNSq9NCZbBvd/iRArCypYvRleS+wMyY=";

            subPackages = [ "cmd/imds-broker" ];

            meta = {
              description = "Vends AWS credentials via IMDSv2 for Docker containers, local dev tools, and AI agents";
              homepage = "https://github.com/jamestelfer/imds-broker";
              license = pkgs.lib.licenses.asl20;
              mainProgram = "imds-broker";
            };
          };

          default = self.packages.${system}.imds-broker;
        };
      });
}
