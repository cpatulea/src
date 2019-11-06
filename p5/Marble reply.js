function setup() {
  createCanvas(400, 400);
  frameRate(20);
  colorMode(HSB);
}

function draw() {
  background(220, 0, 0);
  stroke(frameCount % 255, 100, 100);
  noFill();
  beginShape();
  for (let z = 0; z < 20 * TWO_PI; z += 0.1) {
    let a = z * 50 / 20 + random(0, 6);
    let th = z - 10 * sin(frameCount / 10);
    vertex(200 + a * sin(th), 200 + a * cos(th));
  }
  endShape();
}
