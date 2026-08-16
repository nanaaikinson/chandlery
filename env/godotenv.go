package env

import "github.com/joho/godotenv"

// godotenvLoad wraps godotenv.Load so Load() can be swapped/tested in isolation.
func godotenvLoad(filenames ...string) error {
	return godotenv.Load(filenames...)
}
