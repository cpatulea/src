function initMap() {
  var center = {lat: 20, lng: 0};
  var map = new google.maps.Map(
      document.getElementById('map'), {zoom: 1, center: center,
      controlSize: 20});

  for (var li of document.querySelectorAll('div.results ol li')) {
    var latlng = li.getAttribute('data-latlng').split(',');
    latlng = {lat: parseFloat(latlng[0]), lng: parseFloat(latlng[1])};
    var href = li.querySelector('a').href;

    var marker = new google.maps.Marker({
      position: latlng,
      map: map,
      title: li.querySelector('abbr').innerText
    });
    marker.addListener('click', function (marker, href) {
      return function() {
        new google.maps.InfoWindow({
          content: '<a href="' + href + '" target="_blank">' + marker.title + '</a>'
        }).open(map, marker);
      }
    }(marker, href));
  }
}
