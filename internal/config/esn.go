package config

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
)

func GenerateESN(random io.Reader) (string, error) {
	if random == nil {
		return "", errors.New("random source is nil")
	}
	buffer := make([]byte, 16)
	for attempts := 0; attempts < 16; attempts++ {
		if _, err := io.ReadFull(random, buffer); err != nil {
			return "", err
		}
		value := new(big.Int).SetBytes(buffer)
		if value.Sign() == 0 {
			continue
		}
		decimal := value.Text(10)
		if len(decimal) > 39 {
			return "", fmt.Errorf("generated value exceeds 39 digits")
		}
		return strings.Repeat("0", 39-len(decimal)) + decimal, nil
	}
	return "", errors.New("random source produced zero repeatedly")
}
