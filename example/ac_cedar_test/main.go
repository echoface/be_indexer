package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"

	cedar "github.com/iohub/ahocorasick"
)

func NewMatcher(dictPath string) (*cedar.Matcher, *cedar.Cedar) {
	m := cedar.NewMatcher()
	trie := cedar.NewCedar()

	f, err := os.Open(dictPath)
	if err != nil {
		panic(err)
	}

	line := 0
	r := bufio.NewReader(f)
	for {
		line++
		l, err := r.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		l = bytes.TrimSpace(l)
		m.Insert(l, line)

		if err = trie.Insert(l, line); err != nil {
			panic(err)
		}
	}

	return m, trie
}

func Match(m *cedar.Matcher, key []byte) map[string]interface{} {
	result := map[string]interface{}{}
	resp := m.Match(key)
	for resp.HasNext() {
		items := resp.NextMatchItem(key)
		for _, itr := range items {
			result[string(m.Key(key, itr))] = itr.Value.(int)
		}
	}
	resp.Release()
	return result
}

func main() {
	testCase := []byte("我是一只小白鼠")

	m, trie := NewMatcher("./test1.txt")
	m.Compile()
	r := Match(m, testCase)
	for key, line := range r {
		if value, err := trie.Get([]byte(key)); err != nil {
			panic(err)
		} else {
			fmt.Println("trie hit this value:", value, ", result line:", line)
		}
	}
	fmt.Println("Result:", r)

	m2, trie2 := NewMatcher("./test2.txt")
	m2.Compile()
	r = Match(m2, testCase)
	for key, line := range r {
		if value, err := trie2.Get([]byte(key)); err != nil {
			panic(err)
		} else {
			fmt.Println("trie hit this value:", value, ", result line:", line)
		}
	}
	fmt.Println("Result2:", 2)

	if value, err := trie2.Get([]byte("一")); err != nil {
		panic(err)
	} else {
		fmt.Println("一 trie hit this value:", value)
	}

	if value, err := trie2.Get([]byte("只")); err != nil {
		panic(err)
	} else {
		fmt.Println("只 trie hit this value:", value)
	}
}
