function setup() {
  createCanvas(400, 400);
  frameRate(20);
  colorMode(HSB);
}

function draw() {
  background(220, 0, 0);
  noFill();
  for (let z = 0; z < 20; ++z) {
    let a = 10 * sin(1 + z * frameCount * PI / 70.0);
    stroke((frameCount / 10.0 + z) * 30 % 255, 100, 100);
    beginShape();
    for (let x = 0; x < 400; x += 3) {
      vertex(x, 200 + 10 * a / z * sin((x) / 15) + random(0, 10));
    }
    endShape();
  }
}
