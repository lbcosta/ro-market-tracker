package gnjoytest

// DemoConfig devolve um conjunto de dados fixo e determinístico, pensado para
// os testes de navegador (e utilizável nos testes de Go): cobre um item de
// equipamento com vários anúncios em refinos diferentes, um item comum sem
// refino e um histórico de preço com números escolhidos para que as
// estatísticas de N dias tenham valores exatos e fáceis de afirmar.
//
// O item "Espada Primordial" (itemId 600009) aparece três vezes, em preços
// crescentes e refinos +0, +7 e +10 — é o que permite exercitar a busca do
// menor preço, o filtro por refino específico da watchlist (que precisa
// consultar o detalhe de cada loja candidata até achar o refino pedido) e o
// caso de refino inexistente.
func DemoConfig() Config {
	primordiais := []ShopListItem{
		demoListItem("s-primordial-129", 600009, "Espada Primordial", "weapon", 129999999, "Vendinha do Zé"),
		demoListItem("s-primordial-158", 600009, "Espada Primordial", "weapon", 158000000, "Wololol"),
		demoListItem("s-primordial-299", 600009, "Espada Primordial", "weapon", 299999999, "Só levo oferta"),
	}
	poring := []ShopListItem{
		demoListItem("p-carta-noel", 4005, "Carta Poring Noel", "card", 199999, "PORINGÃO STORE"),
	}

	return Config{
		// Nenhum dos termos abaixo aparece em Searches: são itens que ninguém
		// está anunciando, a situação de quem procura um item para rastrear e
		// não o encontra no mercado atual. Cada um cobre um desfecho da
		// consulta de preços praticados (ver internal/web/history.go).
		// A consulta de preços praticados é feita em duas janelas: o histórico
		// completo ("ALL"), que diz quais itens existem, e os últimos 7 dias,
		// que diz quanto eles custam hoje. Os três termos abaixo cobrem as
		// três combinações possíveis entre elas.
		MarketPrices: map[MarketPriceScope]MarketPriceResult{
			// Todos os itens venderam na última semana. "Rapidez" casa dois
			// itens diferentes — o caso que revelou que a tabela precisa ter
			// uma linha por item, e não uma linha só. Os números da janela de
			// 7 dias são os que o site devolveu de verdade para esse termo; os
			// do histórico completo são mais largos de propósito, para os
			// testes verem qual das duas janelas ganhou.
			{ServerType: "NIDHOGG", SearchWord: "Rapidez", Period: "7"}: {Items: []MarketPriceItem{
				demoMarketPrice(1000125, "Automódulo de M-Rapidez", 3, 5000000, 8666666, 12000000),
				demoMarketPrice(25690, "Módulo de S-Rapidez", 2, 5000000, 7500000, 10000000),
			}},
			{ServerType: "NIDHOGG", SearchWord: "Rapidez", Period: "ALL"}: {Items: []MarketPriceItem{
				demoMarketPrice(1000125, "Automódulo de M-Rapidez", 40, 1000000, 4000000, 20000000),
				demoMarketPrice(25690, "Módulo de S-Rapidez", 30, 900000, 3000000, 18000000),
			}},

			// Nenhum item vendeu na última semana: a janela curta vem vazia e
			// só o histórico completo tem o que mostrar.
			{ServerType: "NIDHOGG", SearchWord: "Bota do Andarilho", Period: "ALL"}: {Items: []MarketPriceItem{
				demoMarketPrice(610003, "Bota do Andarilho", 14, 800000, 1250000, 2000000),
			}},

			// Janelas divergentes, o caso que dava item sumido: o "III" vendeu
			// na última semana e o "II" não, então montar a tabela a partir da
			// janela curta escondia o "II" — que existe e tem histórico. Estes
			// são os números reais dos dois no servidor NIDHOGG.
			{ServerType: "NIDHOGG", SearchWord: "Reformador Primordial", Period: "ALL"}: {Items: []MarketPriceItem{
				demoMarketPrice(100834, "Reformador Primordial III", 8, 38500000, 80187498, 129999999),
				demoMarketPrice(100820, "Reformador Primordial II", 8, 109999998, 222624999, 500000000),
			}},
			{ServerType: "NIDHOGG", SearchWord: "Reformador Primordial", Period: "7"}: {Items: []MarketPriceItem{
				demoMarketPrice(100834, "Reformador Primordial III", 2, 129999988, 129999993, 129999999),
			}},

			// "Elmo Ancestral" não é registrado em nenhum período: item que
			// nunca foi vendido no servidor.
		},
		Searches: map[string]SearchResult{
			// Busca ampla: casa itens diferentes que compartilham a palavra,
			// que é o comportamento do site e o motivo de a watchlist
			// precisar guardar o itemId além do nome.
			"Espada": {Items: append(append([]ShopListItem{}, primordiais...),
				demoListItem("s-citadina", 1147, "Espada Citadina", "weapon", 59000, "sk"),
				demoListItem("s-carta-peixe", 4089, "Carta Peixe-Espada", "card", 4999999, "Cartas baratas"),
			)},
			"Poring": {Items: poring},

			// A watchlist não consulta pelo termo digitado, e sim pelo nome
			// canônico do item que a busca devolveu — então esses nomes
			// também precisam ser buscáveis.
			"Espada Primordial": {Items: primordiais},
			"Carta Poring Noel": {Items: poring},
		},
		Stores: map[string]StoreDetail{
			// Os três anúncios do mesmo item diferem só pelo refino embutido
			// no prefixo "+N" de itemFullName — a única forma de o client
			// saber o refino de um anúncio.
			"s-primordial-129": demoStore("s-primordial-129", 600009, "Espada Primordial[2]", "weapon", 129999999, "Vendinha do Zé", "Zezinho", "prt_mk.gat", "114", "180"),
			"s-primordial-158": demoStore("s-primordial-158", 600009, "+7Espada Primordial[2]", "weapon", 158000000, "Wololol", "mercatung", "prt_mk.gat", "120", "150"),
			"s-primordial-299": demoStore("s-primordial-299", 600009, "+10Espada Primordial[2]", "weapon", 299999999, "Só levo oferta", "Merch One", "prt_mk.gat", "99", "42"),
			"s-citadina":       demoStore("s-citadina", 1147, "Espada Citadina[2]", "weapon", 59000, "sk", "S-k8", "prt_mk.gat", "112", "178"),
			"s-carta-peixe":    demoStore("s-carta-peixe", 4089, "Carta Peixe-Espada", "card", 4999999, "Cartas baratas", "SaynMERC", "prt_mk.gat", "100", "100"),
			"p-carta-noel":     demoStore("p-carta-noel", 4005, "Carta Poring Noel", "card", 199999, "PORINGÃO STORE", "Ferreirinha", "prt_mk.gat", "112", "178"),
		},
		Items: map[string]ItemDetail{
			"s-primordial-129": demoItem("s-primordial-129", 600009, "Espada Primordial", "weapon", 129999999),
			"s-primordial-158": demoItem("s-primordial-158", 600009, "Espada Primordial", "weapon", 158000000),
			"s-primordial-299": demoItem("s-primordial-299", 600009, "Espada Primordial", "weapon", 299999999),
			"s-citadina":       demoItem("s-citadina", 1147, "Espada Citadina", "weapon", 59000),
			"s-carta-peixe":    demoItem("s-carta-peixe", 4089, "Carta Peixe-Espada", "card", 4999999),
			"p-carta-noel":     demoItem("p-carta-noel", 4005, "Carta Poring Noel", "card", 199999),
		},
		Prices: map[int]PriceHistory{
			// Números escolhidos para gerar estatísticas exatas:
			//   qtd total = 4, média ponderada = 7000/4 = 1750,
			//   mínimo = 500, máximo = 5000,
			//   desvio = sqrt(2750000/4) = sqrt(687500) ≈ 829,16 -> "829 z".
			600009: {
				ItemPriceMin: 500,
				ItemPriceMax: 5000,
				DayStatsList: []PriceDayStat{
					{Date: "2026-07-30", MinItemPrice: 1000, MaxItemPrice: 3000, AvgItemPrice: 2000, ItemCnt: 1, TotalCount: 3},
					{Date: "2026-07-29", MinItemPrice: 2000, MaxItemPrice: 4000, AvgItemPrice: 3000, ItemCnt: 1, TotalCount: 3},
					{Date: "2026-07-28", MinItemPrice: 500, MaxItemPrice: 5000, AvgItemPrice: 1000, ItemCnt: 2, TotalCount: 3},
				},
			},
			// Item sem nenhum dia de histórico: o card de detalhe precisa
			// mostrar "Sem histórico de vendas recente" em vez de zeros.
			4005: {},
		},
	}
}

func demoMarketPrice(itemId int, name string, vol int, min, avg, max int64) MarketPriceItem {
	return MarketPriceItem{
		SvrId:           303,
		ItemId:          itemId,
		MapId:           835,
		SSI:             "mp-" + name,
		ItemName:        name,
		DatabaseImgPath: "https://assets.example.invalid/" + name + ".png",
		DatabaseType:    "miscellaneous",
		TotalItemCnt:    vol,
		MinItemPrice:    min,
		MaxItemPrice:    max,
		AvgItemPrice:    avg,
	}
}

func demoListItem(ssi string, itemId int, name, dbType string, price int64, storeName string) ShopListItem {
	return ShopListItem{
		SvrId:              303,
		ItemId:             itemId,
		MapId:              835,
		SSI:                ssi,
		ItemName:           name,
		DatabaseImgPath:    "https://assets.example.invalid/" + ssi + ".png",
		DatabaseType:       dbType,
		StoreName:          storeName,
		ItemPrice:          price,
		ItemCnt:            1,
		SlotMaxCount:       "",
		StoreTypeName:      "BUY",
		ItemSellerCharName: "Vendedor " + ssi,
	}
}

func demoStore(ssi string, itemId int, fullName, dbType string, price int64, storeName, seller, mapName, x, y string) StoreDetail {
	return StoreDetail{
		SvrId:               303,
		SvrName:             "NIDHOGG",
		ItemId:              itemId,
		MapId:               835,
		SSI:                 ssi,
		StoreName:           storeName,
		MapName:             mapName,
		ItemSellerCharName:  seller,
		ItemFullName:        fullName,
		ItemPrice:           price,
		MarketStoreTypeCode: "BUY",
		ItemCnt:             1,
		DatabaseImgPath:     "https://assets.example.invalid/" + ssi + ".png",
		DatabaseType:        dbType,
		Xpos:                x,
		Ypos:                y,
	}
}

func demoItem(ssi string, itemId int, name, dbType string, price int64) ItemDetail {
	return ItemDetail{
		SvrId:           303,
		ItemId:          itemId,
		ItemName:        name,
		ItemPrice:       price,
		MapId:           835,
		SSI:             ssi,
		ItemType:        dbType,
		HasDatabaseItem: true,
		DatabaseImgPath: "https://assets.example.invalid/" + ssi + ".png",
		DatabaseType:    dbType,
	}
}
