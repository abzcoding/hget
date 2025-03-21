[![Build Status](https://api.travis-ci.com/abzcoding/hget.svg)](https://travis-ci.com/abzcoding/hget)
[![Scrutinizer Code Quality](https://scrutinizer-ci.com/g/abzcoding/hget/badges/quality-score.png?b=master)](https://scrutinizer-ci.com/g/abzcoding/hget/?branch=master)
[![Maintainability](https://api.codeclimate.com/v1/badges/936e2aacab5946478295/maintainability)](https://codeclimate.com/github/abzcoding/hget/maintainability)
[![Codebeat](https://codebeat.co/badges/ea357ae8-4d84-4599-bff7-cffc4f28fd67)](https://codebeat.co/projects/github-com-abzcoding-hget-master)

# hget
![](https://i.gyazo.com/641166ab79e196e35d1a0ef3f9befd80.png)

### Features
- Fast (multithreading & stuff)
- Ability to interrupt/resume (task mangement)
- Support for proxies( socks5 or http)
- Bandwidth limiting
- You can give it a file that contains list of urls to download

### Install

```bash
$ go get github.com/abzcoding/hget
$ cd $GOPATH/src/github.com/abzcoding/hget
$ make clean install
```

Binary file will be built at ./bin/hget, you can copy to /usr/bin or /usr/local/bin and even `alias wget hget` to replace wget totally :P

### Usage

```bash
hget [-n parallel] [-skip-tls false] [-rate bwRate] [-proxy proxy_server] [-file filename] [URL] # to download url, with n connections, and not skip tls certificate
hget - resume TaskName # to resume task
hget -proxy "127.0.0.1:12345" URL # to download using socks5 proxy
hget -proxy "http://sample-proxy.com:8080" URL # to download using http proxy
hget -file sample.txt # to download a list of urls
hget -n 4 -rate 100KB URL # to download using 4 threads & limited to 100KB per second

# real world example
hget -n 16 -rate 10MiB "https://old-releases.ubuntu.com/releases/22.04.1/ubuntu-22.04-beta-live-server-amd64.iso"
# resuming a stopped download
hget -resume "ubuntu-22.04-beta-live-server-amd64.iso"
```

### Cleanup

you might wanna cleanup `~/.hget` folder after downloading a lot of unfinished/cancelled downloads

### Help
```
[I] ➜ hget -h
Usage of hget:
  -file string
        path to a file that contains one URL per line
  -n int
        number of connections (default 12)
  -proxy string
        proxy for downloading, e.g. -proxy '127.0.0.1:12345' for socks5 or -proxy 'http://proxy.com:8080' for http proxy
  -rate string
        bandwidth limit during download, e.g. -rate 10kB or -rate 10MiB
  -resume string
        resume download task with given task name (or URL)
  -skip-tls
        skip certificate verification for https (default false)
```

To interrupt any on-downloading process, just ctrl-c or ctrl-d at the middle of the download, hget will safely save your data and you will be able to resume later

### Download
![](https://i.gyazo.com/89009c7f02fea8cb4cbf07ee5b75da0a.gif)

### Resume
![](https://i.gyazo.com/caa69808f6377421cb2976f323768dc4.gif)
