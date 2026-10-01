Output docs/demos/upload.gif
Output docs/demos/upload.mp4
Set Shell "bash"
Set WaitTimeout 5m
Set FontFamily "Menlo"
Set FontSize 18
Set Width 1200
Set Height 760
Set Padding 24
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set TypingSpeed 65ms
Set Framerate 20
Hide
Type@1ms "unset NO_COLOR; export TERM=xterm-256color COLORTERM=truecolor; export PS1='$ '; export PATH=$PWD/bin:$PATH; python3 docs/demos/server.py init upload; export YZ_CONFIG_DIR=$PWD/docs/demos/.config-upload YZ_API_BASE_URL=http://127.0.0.1:8766 YZ_R2_ENDPOINT=http://127.0.0.1:8766; cd docs/demos; clear"
Enter
Sleep 500ms
Show
Type "yz hello.txt"
Enter
Sleep 8s
Enter
Sleep 2s
