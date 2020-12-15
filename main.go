package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
)

var (
	ctx  = context.Background()
	TTL  = time.Minute * 20
	conn = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})
)

func main() {
	// The user name is the only argument, so we'll grab that here
	if len(os.Args) != 2 {
		fmt.Println("Usage: goredchat username")
		os.Exit(1)
	}
	username := os.Args[1]

	defer conn.Close()

	// We make a key and set it with the username as a value and a time to live
	// this will be the lock on the username and if we can't set it, its a name clash

	userkey := "online." + username
	val, err := conn.Set(ctx, userkey, username, TTL).Result()

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if val == "" {
		fmt.Println("User already online")
		os.Exit(1)
	}

	valint, err := conn.SAdd(ctx, "users", username).Result()

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if valint == 0 {
		fmt.Println("User still in online set")
		os.Exit(1)
	}

	// A ticker will let us update our presence on the Redis server
	tickerChan := time.NewTicker(time.Second * 60).C

	// Now we create a channel and go routine that'll subscribe to our published messages
	// We'll give it its own connection because subscribes like to have their own connection
	subChan := make(chan string)
	go func() {

		psc := conn.Subscribe(ctx, "messages")
		_, err := psc.Receive(ctx)
		if err != nil {
			panic(err)
		}
		// Go channel which receives messages.
		ch := psc.Channel()
		for v := range ch {
			subChan <- string(v.Payload)
		}
	}()

	// Now we'll make a simple channel and go routine that listens for complete lines from the user
	// When a complete line is entered, it'll be delivered to the channel.
	sayChan := make(chan string)
	go func() {
		prompt := username + ">"
		bio := bufio.NewReader(os.Stdin)
		for {
			fmt.Print(prompt)
			line, _, err := bio.ReadLine()
			if err != nil {
				fmt.Println(err)
				sayChan <- "/exit"
				return
			}
			sayChan <- string(line)
		}
	}()

	conn.Publish(ctx, "messages", username+" has joined")

	chatExit := false

	for !chatExit {
		select {
		case msg := <-subChan:
			fmt.Println(msg)
		case <-tickerChan:
			val, err = conn.Set(ctx, userkey, username, TTL).Result()
			if err != nil || val == "" {
				fmt.Println("Heartbeat set failed")
				chatExit = true
			}
		case line := <-sayChan:
			if line == "/exit" {
				chatExit = true
			} else if line == "/who" {
				names, _ := conn.SMembers(ctx, "users").Result()

				for _, name := range names {
					fmt.Println(name)
				}
			} else {
				conn.Publish(ctx, "messages", username+":"+line)
			}
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	// We're leaving so let's delete the userkey and remove the username from the online set
	conn.Del(ctx, userkey)
	conn.SRem(ctx, "users", username)
	conn.Publish(ctx, "messages", username+" has left")

}
