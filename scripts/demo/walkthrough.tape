Output walk-terminal.mp4
Set Width 1280
Set Height 800
Set FontSize 17
Set Padding 22
Set Theme "Dracula"
Set TypingSpeed 45ms

Type "# gRPC Service — end-to-end walkthrough"
Enter
Sleep 700ms
Type "# 1) start the authenticated server"
Enter
Type "source scripts/demo/wserve.sh"
Enter
Sleep 2s
Type "# 2) readiness probe"
Enter
Type "curl -s localhost:18080/readyz; echo"
Enter
Sleep 1400ms
Type "# 3) run the client (unary + streaming RPCs)"
Enter
Type "bash scripts/demo/wclient.sh"
Enter
Sleep 4s
Type "# 4) Prometheus RED metrics"
Enter
Type "bash scripts/demo/wmetrics.sh"
Enter
Sleep 2600ms
