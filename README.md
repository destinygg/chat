
# Important Notice
As of 6/28/2022 this repository is no longer being publicly maintained. Code in it's current state is left for historic preservation, but will no longer be receiving updates, security patches, or support.

Licensing inquiries can be submitted via email to contact@destiny.gg

The Destiny Chat Back-End
===========

The chat back-end for destiny.gg, written in Go, based on Golem (http://github.com/trevex/golem) 

Licensed under the Apache License, Version 2.0 

http://www.apache.org/licenses/LICENSE-2.0.html

This is my (sztanpet's) first not-so-tiny Go project, so if there is anything that could be improved, please do tell.

### How to Build & Run

1. Clone this repo.

```
$ git clone https://github.com/destinygg/chat.git
```

2. Navigate into the project folder.

```
$ cd chat
```

3. Download all dependencies.

```
$ go mod download
```

4. Verify dependency checksums to ensure nothing fishy is going on.

```
$ go mod verify
```

5. Build the binary.

```
$ go build -o chat
```

6. Run the binary.

```
$ ./chat
```

If a `settings.cfg` file doesn't exist, one will be created on first run. Modify it to your liking and run the binary again when done.
