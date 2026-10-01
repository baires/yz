Output docs/demos/setup.gif
Output docs/demos/setup.mp4
Set Shell "bash"
Set WaitTimeout 5m
Set FontFamily "Menlo"
Set FontSize 18
Set Width 1200
Set Height 1000
Set Padding 24
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set TypingSpeed 65ms
Set Framerate 20
Hide
Type@1ms "unset NO_COLOR; export TERM=xterm-256color COLORTERM=truecolor; export PS1='$ '; export PATH=$PWD/bin:$PATH; python3 docs/demos/server.py init setup; export YZ_CONFIG_DIR=$PWD/docs/demos/.config-setup YZ_API_BASE_URL=http://127.0.0.1:8766 YZ_R2_ENDPOINT=http://127.0.0.1:8766; clear"
Enter
Sleep 500ms
Show
Type "yz setup"
Enter
Wait+Screen /Choose a bucket/
Sleep 2s
Sleep 1s
Enter
Wait+Screen /Paste your Access Key ID/
Sleep 2s
Type@35ms "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
Sleep 1s
Enter
Sleep 1s
Type@25ms "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
Sleep 1s
Enter
Sleep 5s
Sleep 3s
Enter
Sleep 1s
