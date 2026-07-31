package gnjoy

import (
	"errors"
	"strings"
	"testing"
)

func TestParseFlightObject(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		keys     []string
		wantKey  string
		wantVal  any
		wantErr  error
		wantNone bool
	}{
		{
			name: "objeto no primeiro nível",
			body: "1:{\"data\":{\"a\":1},\"success\":true}\n",
			keys: []string{"data", "success"},
			// O objeto devolvido é o envelope inteiro.
			wantKey: "success",
			wantVal: true,
		},
		{
			name: "objeto aninhado dentro de um array",
			body: `10:["$","$L12",null,{"list":[{"itemId":1}],"totalCount":1}]` + "\n",
			keys: []string{"list", "totalCount"},
			// json decodifica números como float64.
			wantKey: "totalCount",
			wantVal: float64(1),
		},
		{
			name: "objeto aninhado como valor de outra chave",
			body: `2:{"props":{"children":{"list":[],"totalCount":0}}}` + "\n",
			keys: []string{"list", "totalCount"},

			wantKey: "totalCount",
			wantVal: float64(0),
		},
		{
			name: "ignora linhas que não são JSON e as sem separador",
			body: "3:I[59665,[],\"OutletBoundary\"]\n" +
				"linha solta sem dois pontos\n" +
				"a:$undefined\n" +
				"1:{\"data\":{},\"success\":true}\n",
			keys:    []string{"data", "success"},
			wantKey: "success",
			wantVal: true,
		},
		{
			name:    "nenhuma linha tem os campos procurados",
			body:    "0:{\"a\":1}\n9:[[\"$\",\"meta\"]]\n",
			keys:    []string{"data", "success"},
			wantErr: ErrFieldsNotFound,
		},
		{
			name:    "corpo vazio",
			body:    "",
			keys:    []string{"data"},
			wantErr: ErrFieldsNotFound,
		},
		{
			name:    "objeto com apenas parte das chaves não serve",
			body:    "1:{\"data\":{}}\n",
			keys:    []string{"data", "success"},
			wantErr: ErrFieldsNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := parseFlightObject([]byte(tt.body), tt.keys...)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("erro = %v, quero %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlightObject: %v", err)
			}
			if got := obj[tt.wantKey]; got != tt.wantVal {
				t.Errorf("obj[%q] = %v (%T), quero %v (%T)", tt.wantKey, got, got, tt.wantVal, tt.wantVal)
			}
		})
	}
}

// TestParseFlightObjectLinhaMuitoLonga cobre o motivo de o scanner ter um
// buffer customizado: uma busca com muitos anúncios cabe em uma única linha
// do payload, bem acima do limite padrão de 64 KiB do bufio.Scanner.
func TestParseFlightObjectLinhaMuitoLonga(t *testing.T) {
	nome := strings.Repeat("A", 200_000)
	body := `10:["$","$L12",null,{"list":[{"itemName":"` + nome + `"}],"totalCount":1}]` + "\n"

	obj, err := parseFlightObject([]byte(body), "list", "totalCount")
	if err != nil {
		t.Fatalf("parseFlightObject: %v", err)
	}
	list, ok := obj["list"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("list = %v, quero uma lista de 1 item", obj["list"])
	}
	item := list[0].(map[string]any)
	if got := item["itemName"].(string); len(got) != len(nome) {
		t.Errorf("len(itemName) = %d, quero %d", len(got), len(nome))
	}
}

func TestDecodeInto(t *testing.T) {
	obj := map[string]any{"svrId": 303, "ssi": "abc"}

	var target struct {
		SvrId int    `json:"svrId"`
		SSI   string `json:"ssi"`
	}
	if err := decodeInto(obj, &target); err != nil {
		t.Fatalf("decodeInto: %v", err)
	}
	if target.SvrId != 303 || target.SSI != "abc" {
		t.Errorf("target = %+v, quero {303 abc}", target)
	}
}

func TestDecodeIntoTipoIncompativel(t *testing.T) {
	obj := map[string]any{"svrId": "não é número"}

	var target struct {
		SvrId int `json:"svrId"`
	}
	err := decodeInto(obj, &target)
	if err == nil {
		t.Fatal("esperava erro, veio nil")
	}
	if !strings.Contains(err.Error(), "decodificando objeto") {
		t.Errorf("erro = %v, quero menção à decodificação", err)
	}
}
