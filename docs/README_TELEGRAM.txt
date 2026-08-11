AVISO DA WATCHLIST NO TELEGRAM (OPCIONAL)
===============================================================================

  Além do aviso na página (e da notificação do sistema, se você permitir),
  dá para receber uma mensagem no Telegram toda vez que a watchlist bater o
  alvo — com o nome do item, o preço, a loja e o comando para chegar até
  ela.

  É opcional. Sem configurar nada, o programa continua funcionando
  normalmente, só sem esse aviso.


COMO CONFIGURAR

  1. Crie um bot
     Abra uma conversa com @BotFather no Telegram e mande /newbot. Siga as
     instruções (ele vai pedir um nome e um nome de usuário terminado em
     "bot"). No final ele te dá um token, parecido com isto:

       123456789:ABCDefGhIJKlmNoPQRsTUVwxyZ

  2. Fale com o seu bot
     Procure pelo nome de usuário que você escolheu no passo 1 e mande
     qualquer mensagem para ele (um "oi" já basta).

  3. Descubra o seu chat_id
     No navegador, abra o endereço abaixo, trocando <TOKEN> pelo token do
     passo 1:

       https://api.telegram.org/bot<TOKEN>/getUpdates

     Procure por "chat":{"id":NÚMERO — esse número (pode vir negativo) é o
     seu chat_id.

  4. Preencha o arquivo "telegram.txt"
     Ele veio junto deste README, na mesma pasta do programa. Abra com o
     bloco de notas e preencha as duas linhas:

       TELEGRAM_BOT_TOKEN=o token do passo 1
       TELEGRAM_CHAT_ID=o número do passo 3

     Salve o arquivo.

  5. Reinicie o programa
     Feche a janela do terminal (ou Ctrl+C) e abra de novo. O arquivo só é
     lido na hora que o programa liga.

  Pronto — o próximo aviso da watchlist chega no Telegram também.


PROBLEMAS

  Se a mensagem não chegar, confira se copiou o token e o chat_id certos
  (sem espaços a mais) e se o programa foi mesmo reiniciado depois de salvar
  o arquivo. Qualquer outro problema, veja "PROBLEMAS E SUGESTÕES" no
  LEIAME.txt.
