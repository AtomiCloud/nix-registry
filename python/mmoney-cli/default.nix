{ nixpkgs, pyPkgs ? nixpkgs.pkgs.python3Packages }:
with pyPkgs;
buildPythonPackage rec {
  pname = "mmoney-cli";
  version = "0.1.0";
  format = "pyproject";

  # PyPI publishes the PEP-625 sdist with an underscore-normalised name
  # (mmoney_cli-${version}.tar.gz), so fetch using the underscore pname.
  src = fetchPypi {
    pname = "mmoney_cli";
    inherit version;
    sha256 = "sha256-qDfc5uJG06ZW0JvxnnzoX3nJ51mvLkaD6UB7jcw05t4=";
  };

  build-system = [ setuptools ];

  # Relax the pinned lower bounds so we can use the nixpkgs-provided versions.
  pythonRelaxDeps = true;

  propagatedBuildInputs = [ click keyring monarchmoneycommunity ];

  checkPhase = ''
    echo "no test!"
  '';
}
