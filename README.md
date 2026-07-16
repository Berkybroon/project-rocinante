# project-rocinante

This is a small Go program that walks a folder tree and writes a simple HTML report you can open in a browser. It shows the folders and files in a tree view, with folders that can be expanded and collapsed.

It is handy if you want a quick way to see what is taking up space on a drive or folder, especially when you want something a bit more readable than a plain terminal listing.

If you're an IT administrator, and don't want to interrupt a user to run TreeSize/WinDirStat on their machine, this program should accomplish more or less the same thing, entirely from the CLI. This is assuming your RMM allows remote terminal & file download.

## How it works

The program scans the directory you give it, builds a tree of folders and files, and writes an HTML report with a basic expandable layout. It also keeps the report fairly lightweight by showing only the biggest files in each folder.

## Usage

Complining on your own machine requires Go to be installed (https://go.dev/doc/install)

Build it with:

```bash
go build main.go
```

Then run it like this:

```bash
./main.exe C:\ report.html
```

Or for the current folder:

```bash
./main.exe . report.html
```

## Notes

If you run it without administrator privileges on Windows, some folders may be skipped or show as incomplete. The report will warn you about that.

