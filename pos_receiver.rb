# frozen_string_literal: true

require "bundler"
Bundler.require

require "time"

class Position
  attr_accessor :uuid # "uuid"
  attr_accessor :timestamp # "timestamp"
  attr_accessor :paused # "paused"
  attr_accessor :latitude # "latitude"
  attr_accessor :longitude # "longitude"
  attr_accessor :magnetic_heading # "magneticHeading"
  attr_accessor :true_heading # "trueHeading"
  attr_accessor :track # "track"
  attr_accessor :actual_altitude # "actualAltitude"
  attr_accessor :height_agl # "heightAGL"
  attr_accessor :groundspeed # "groundspeed"
  attr_accessor :indicated_airspeed # "indicatedAirspeed"
  attr_accessor :indicated_airspeed2 # "indicatedAirspeed2"
  attr_accessor :true_airspeed # "trueAirspeed"
  attr_accessor :mach_airspeed # "machAirspeed"

  def initialize(data)
    self.uuid                = data.fetch("uuid")
    self.timestamp           = Time.parse(data.fetch("timestamp"))
    self.paused              = data.fetch("paused")
    self.latitude            = data.fetch("latitude")
    self.longitude           = data.fetch("longitude")
    self.magnetic_heading    = data.fetch("magneticHeading")
    self.true_heading        = data.fetch("trueHeading")
    self.track               = data.fetch("track")
    self.actual_altitude     = data.fetch("actualAltitude")
    self.height_agl          = data.fetch("heightAGL")
    self.groundspeed         = data.fetch("groundspeed")
    self.indicated_airspeed  = data.fetch("indicatedAirspeed")
    self.indicated_airspeed2 = data.fetch("indicatedAirspeed2")
    self.true_airspeed       = data.fetch("trueAirspeed")
    self.mach_airspeed       = format("%.02f", data.fetch("machAirspeed"))
  end
end

# curl -XPOST localhost:4567/positions -H "Content-Type: application/json" -d '[{ "a": "b" }]'
post "/positions" do
  body = request.body.read
  data = MultiJson.load(body)
  data = data.map { Position.new(it) }
  data.each do |pos|
    line = "UUID: #{pos.uuid}, time: #{pos.timestamp.strftime('%H:%I:%S')}, lat: #{pos.latitude}, lon: #{pos.longitude}, alt: #{pos.actual_altitude} ft, hdg: #{pos.magnetic_heading}, ground: #{pos.groundspeed} kias, true: #{pos.true_airspeed} kias, mach: #{pos.mach_airspeed}M"
    puts line
  end
  status 204
end
