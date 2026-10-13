Output docs/demos/site.gif
Output docs/demos/site.mp4
Set Shell "bash"
Set WaitTimeout 5m
Set FontFamily "Menlo"
Set FontSize 18
Set Width 1200
Set Height 900
Set Padding 24
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set TypingSpeed 65ms
Set Framerate 20
Hide
Type@1ms "unset NO_COLOR; export TERM=xterm-256color COLORTERM=truecolor; export PS1='$ '; export PATH=$PWD/bin:$PATH; python3 docs/demos/server.py init site; export YZ_CONFIG_DIR=$PWD/docs/demos/.config-site YZ_API_BASE_URL=http://127.0.0.1:8766 YZ_R2_ENDPOINT=http://127.0.0.1:8766; cd docs/demos; yz hello.txt </dev/null >/dev/null 2>&1; clear"
Enter
Sleep 2s
Show
Type "ls site"
Enter
Sleep 1500ms
Type "yz site site"
Enter
Sleep 7s
Enter
Sleep 1500ms
Type "yz list"
Enter
Wait+Screen /d delete/
Sleep 2500ms
Type "d"
Wait+Screen /y to confirm/
Sleep 2500ms
Type "y"
Wait+Screen /deleted site/
Sleep 3s
Type "q"
Sleep 1s
