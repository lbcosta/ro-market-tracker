package web

import (
	"fmt"
	"strings"
)

// naviCommand monta o comando de cliente "/navi <mapa>.gat <x>/<y>" que o RO
// usa para traçar rota até um ponto do mapa. mapName vem do detalhe da loja
// já com a extensão do arquivo de mapa (ex.: "prt_mk.gat"); o comando exige
// essa extensão, então ela só é adicionada aqui como rede de segurança caso
// falte.
func naviCommand(mapName, x, y string) string {
	if !strings.HasSuffix(mapName, ".gat") {
		mapName += ".gat"
	}
	return fmt.Sprintf("/navi %s %s/%s", mapName, x, y)
}
