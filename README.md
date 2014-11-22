The Destiny Chat Back-End
===========

The chat back-end for destiny.gg, written in Go, based on Golem (http://github.com/trevex/golem) 

Licensed under the Apache License, Version 2.0 

http://www.apache.org/licenses/LICENSE-2.0.html

This is my first not-so-tiny Go project, so if there is anything that could be improved, please do tell.

=== How to Build:

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

