package refresh

// CleanupResult counts ciphertexts cleared while preserving all historical metadata.
type CleanupResult struct{ Stages, Versions int }

// KeyReference counts remaining credential ciphertext dependencies on one deployment key.
// Other modules sharing the keyring must be inventoried separately before retiring it.
type KeyReference struct {
	KeyVersion       int
	Versions, Stages int64
}
