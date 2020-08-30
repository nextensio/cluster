# minion
Humble workers

# Running test for a particular module
cd nxts.io/minion/http/parse
go test -v

# Running a program
cd nxts.io
go run minion/minion.go

# debug a program

https://www.jamessturtevant.com/posts/Using-the-Go-Delve-Debugger-from-the-command-line/

go get -u github.com/derekparker/delve/cmd/dlv
export PATH=$PATH:$GOPATH/bin
which dlv
/home/davi/work/go/bin/dlv
dlv version
Delve Debugger
Version: 1.2.0
Build: cce377066abcd0342ecd8f4c490ab943ef2f61db
dlv debug nxts.io/minion
Type 'help' for list of commands.
(dlv) b main.main
Breakpoint 1 set at 0x79e97b for main.main() ./nxts.io/minion/minion.go:28
(dlv) c
> main.main() ./nxts.io/minion/minion.go:28 (hits goroutine(1):1 total:1) (PC: 0x79e97b)
    23:	    //"nxts.io/minion/http/parse"
    24:	)
    25:	
    26:	var services [common.MaxService]string
    27:	
=>  28:	func main() {
    29:	    //logger, _ := zap.NewProduction()
    30:	    logger, _ := zap.NewDevelopment()
    31:	    defer logger.Sync()
    32:	    sugar := logger.Sugar()
    33:	    args.ArgHandler(sugar)
