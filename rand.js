function random() {
  numbs = []
  for (let i = 0; i < 5; i++) {
    numbs.push(Math.floor(Math.random() * 10))
  }
  console.log(numbs)
}

random()
