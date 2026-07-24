package gnjoy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrFieldsNotFound é retornado quando nenhuma linha do payload "Flight"
// contém um objeto com todas as chaves procuradas.
var ErrFieldsNotFound = errors.New("gnjoy: campos esperados não encontrados na resposta")

// parseFlightObject varre um corpo de resposta no formato RSC Flight
// (linhas "<id>:<valor>") e devolve o primeiro objeto JSON, em qualquer
// nível de aninhamento, que contenha todas as chaves em requiredKeys.
//
// A maioria das linhas do payload não é JSON válido (são referências de
// módulo/erro do Next.js, ex: `3:I[59665,[],"OutletBoundary"]`); essas
// linhas são ignoradas silenciosamente. Os IDs das linhas variam a cada
// build do site, então não há como confiar em uma posição fixa — por isso a
// busca é feita pelo formato dos dados, não pelo índice da linha.
func parseFlightObject(body []byte, requiredKeys ...string) (map[string]any, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		idx := bytes.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		value := line[idx+1:]

		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			continue
		}
		if found := findObjectWithKeys(decoded, requiredKeys); found != nil {
			return found, nil
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("gnjoy: lendo resposta: %w", err)
	}
	return nil, ErrFieldsNotFound
}

func findObjectWithKeys(v any, keys []string) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		if hasAllKeys(t, keys) {
			return t
		}
		for _, child := range t {
			if found := findObjectWithKeys(child, keys); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range t {
			if found := findObjectWithKeys(child, keys); found != nil {
				return found
			}
		}
	}
	return nil
}

func hasAllKeys(m map[string]any, keys []string) bool {
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

func decodeInto(obj map[string]any, target any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("gnjoy: recodificando objeto: %w", err)
	}
	if err := json.Unmarshal(b, target); err != nil {
		return fmt.Errorf("gnjoy: decodificando objeto: %w", err)
	}
	return nil
}
