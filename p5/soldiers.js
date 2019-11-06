let i = 0;

function setup() {
  createCanvas(400, 400);
  colorMode(HSB);
  strokeWeight(4);
}

function draw() {
  background(0, 100, 0);
  i = (i + 1) % 400;

  for (let x = -400; x < 400; x += 20) {
    let xx = i + x;
    stroke(x, 200, 100);
    line(
      xx + 5 * sin(xx / 10),
      100 + 30 * sin(xx / 10),
      xx + 5 * sin(xx / 10 * 2 + Math.PI),
      250 + 50 * sin(xx / 10 / 2)
    );
  }
}
