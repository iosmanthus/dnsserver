{
  description = "A simple dnsproxy based on CoreDNS";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/master";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem
      (
        system:
          let
            pkgs = nixpkgs.legacyPackages.${system};
          in
            {
              devShell = pkgs.mkShell {
                hardeningDisable = [ "all" ];
                buildInputs = with pkgs;[ git go_1_17 gcc gnumake golangci-lint ];
              };
            }
      );
}
