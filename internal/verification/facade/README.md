# Facade verification

This package owns black-box verification of the public root `Client` and its
`EthClient` view. The test files are kept unchanged during structural moves;
only their package directory changes.

The narrow `internal/rpcsample` link preserves the existing replay fixture
path used by the moved tests. It points to this repository's shared
`internal/rpcsample` directory and must not point outside the repository.
