The Destiny Chat Back-End
===========

The chat back-end for destiny.gg, written in Go, based on Golem (http://github.com/trevex/golem) 

Licensed under the Apache License, Version 2.0 

http://www.apache.org/licenses/LICENSE-2.0.html

This is my first not-so-tiny Go project, so if there is anything that could be improved, please do tell.

## How to Build:

`go get` each package listed in main.go

fix the redis library to this specific commit with:

    cd $GOPATH/src/github.com/vmihailenco/redis
    
    git checkout 28a881e7240a23c43ceee5a9e0c9a56da2da3db3

redis/v2 changed the function argument style for creating a new tcp client

this is the parent commit that we we depend on:

https://github.com/vmihailenco/redis/commit/28a881e7240a23c43ceee5a9e0c9a56da2da3db3

this is the commit that broke our code:

https://github.com/vmihailenco/redis/commit/ce34e39219f360baedf597e03f0a9c938bce59dc

this whole thing won't be an issue any more when we vendor the dependencies with something like godep

## build

once you've installed all the dependencies, and made that tweak to the redis library, you should be able to build the code base

    ./build

NOTE: the ./build procedure will create a settings.cfg file if you don't already have it.

## set up redis:

Install redis with the default settings from the official redis website redis.io 
Any recent version of redis should work.

## set up mysql:

Install mysql according to google's "install mysql $YOUR_OPERATING_SYSTEM_HERE". 
Any recent version of mysql-server should work.

If you want to use the default configuration written from main.go you'll need to create a user called 'username' with password 'password' with access to a database called 'destinygg'

**(aside: You might need to set the time_zone queryparam to 'UTC' instead of '+00:00' if you have the same issues that I (@hayksaakian) had)**

# running

    ./chat

will execute the compiled go code produced from the ./build script. It should complain if there are obvious problems with your setup, like a misconfigured mysql database, or no/bad redis.

    ./run

will run a script that compiles and runs the same go code, but with an additional (-race) flag that will then:

  enable data race detection.
    Supported only on linux/amd64, darwin/amd64 and windows/amd64.

**go note: go run == go build -o /tmp/somefile && /tmp/somefile according to https://botbot.me/freenode/go-nuts/2014-11-13/?page=3
