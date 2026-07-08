{ nixpkgs, pyPkgs ? nixpkgs.pkgs.python3Packages }:
with pyPkgs;
buildPythonPackage rec {
  pname = "codemagic-cli-tools";
  version = "0.68.0";
  format = "wheel";

  # Only a wheel is published to PyPI for this version (no sdist), so fetch the
  # py3 wheel. fetchPypi builds the wheel filename from the underscore-normalised
  # name: codemagic_cli_tools-${version}-py3-none-any.whl
  src = fetchPypi {
    pname = "codemagic_cli_tools";
    inherit version format;
    dist = "py3";
    python = "py3";
    sha256 = "sha256-+0xpWuH+FqWjUBbk0lN+sFWSy9L5+yJU0DzQflrdGbM=";
  };

  # Relax the pinned lower bounds so we can use the nixpkgs-provided versions.
  pythonRelaxDeps = true;

  propagatedBuildInputs = [
    cryptography
    google-api-python-client
    httplib2
    oauth2client
    packaging
    psutil
    pyjwt
    python-dateutil
    requests
  ];

  checkPhase = ''
    echo "no test!"
  '';
}
