# frozen_string_literal: true

require "bundler"
Bundler.require

# https://developer.x-plane.com/article/x-plane-web-api/
# https://developer.x-plane.com/datarefs/
# For WS: websocket-client-simple?
API_REST_BASE_URL = "http://localhost:8086/api"
API_V3_REST_BASE_URL = "http://localhost:8086/api/v3"
API_V3_WEBSOCKET_BASE_URL = "ws://localhost:8086/api/v3"

class ApiError < StandardError; end

def api_v3_available?
  capabilities = list_capabilites
  capabilities.fetch("api").fetch("versions").include?("v3")
end

# {"api" => {"versions" => ["v1", "v2", "v3"]}, "x-plane" => {"version" => "12.4.3"}}
def list_capabilites
  request = Typhoeus::Request.new(
    "#{API_REST_BASE_URL}/capabilities",
    headers: {
      "accept" => "application/json"
    }
  )

  run_request(request)
end

def list_datarefs
  request = Typhoeus::Request.new(
    "#{API_V3_REST_BASE_URL}/datarefs",
    headers: {
      "accept" => "application/json"
    }
  )

  run_request(request)
end

def bind_datarefs(datarefs, datarefs_map)
  datarefs_map.each do |_, item|
    dataref = datarefs.fetch("data").find { |d| d.fetch("name") == item.fetch(:name) }
    raise "Could not find dataref #{item.inspect}" unless dataref

    item[:id] = dataref.fetch("id")
  end
end

def fetch_current_data(datarefs_map)
  {
    latitude: current_latitude(datarefs_map),
    longitude: current_longitude(datarefs_map),
    heading: current_magnetic_heading(datarefs_map),
    true_heading: current_true_heading(datarefs_map),
    track: current_track(datarefs_map),
    actual_altitude: current_actual_altitude(datarefs_map),
    height_agl: current_height_agl(datarefs_map),
    groundspeed: current_groundspeed(datarefs_map),
    indicated_airspeed: current_indicated_airspeed(datarefs_map),
    indicated_airspeed2: current_indicated_airspeed2(datarefs_map),
    true_airspeed: current_true_airspeed(datarefs_map),
    mach_airspeed: current_mach_airspeed(datarefs_map),
    # TODO: Add FPS, accel etc
  }
end

def current_latitude(datarefs_map)
  id = datarefs_map.fetch(:latitude).fetch(:id)
  get_dataref_value(id).to_f
end

def current_longitude(datarefs_map)
  id = datarefs_map.fetch(:longitude).fetch(:id)
  get_dataref_value(id).to_f
end

def current_magnetic_heading(datarefs_map)
  id = datarefs_map.fetch(:mag_psi).fetch(:id)
  get_dataref_value(id).to_f.round # true deg
end

def current_true_heading(datarefs_map)
  id = datarefs_map.fetch(:true_psi).fetch(:id)
  get_dataref_value(id).to_f.round # true deg
end

def current_track(datarefs_map)
  id = datarefs_map.fetch(:hpath).fetch(:id)
  get_dataref_value(id).to_f.round # true deg
end

def current_actual_altitude(datarefs_map)
  id = datarefs_map.fetch(:elevation).fetch(:id)
  (get_dataref_value(id).to_f  * 3.28084).round # m MSL * 3.28084 feet
end

def current_height_agl(datarefs_map)
  id = datarefs_map.fetch(:y_agl).fetch(:id)
  (get_dataref_value(id).to_f  * 3.28084).round # m MSL * 3.28084 feet
end

def current_groundspeed(datarefs_map)
  id = datarefs_map.fetch(:groundspeed).fetch(:id)
  (get_dataref_value(id).to_f * 1.94384).round # m/s * 1.94384 kts
end

def current_indicated_airspeed(datarefs_map)
  id = datarefs_map.fetch(:indicated_airspeed).fetch(:id)
  get_dataref_value(id).to_f.round # kts
end

def current_indicated_airspeed2(datarefs_map)
  id = datarefs_map.fetch(:indicated_airspeed2).fetch(:id)
  get_dataref_value(id).to_f.round # kts
end

def current_true_airspeed(datarefs_map)
  id = datarefs_map.fetch(:true_airspeed).fetch(:id)
  (get_dataref_value(id).to_f * 1.94384).round # m/s * 1.94384 kts
end

def current_mach_airspeed(datarefs_map)
  id = datarefs_map.fetch(:mach_airspeed).fetch(:id)
  get_dataref_value(id).to_f.round(3) # 0.XX
end

def get_dataref_value(id)
  request = Typhoeus::Request.new(
    "#{API_V3_REST_BASE_URL}/datarefs/#{id}/value",
    headers: {
      "accept" => "application/json"
    }
  )

  data = run_request(request)
  data.fetch("data")
end

def run_request(request)
  response = request.run
  check_response(response)

  if response.headers["content-type"].include?("application/json")
    MultiJson.load(response.body)
    # File.write("data.json", MultiJson.dump(MultiJson.load(response.body), pretty: true))
  else
    response.body
  end
end

def check_response(response)
  return if response.success?

  puts response.effective_url
  puts "Request failed (status: #{response.code})"
  puts response.body if !response.body.empty? && response.headers["content-type"].include?("application/json")

  # binding.pry

  raise ApiError, "request failed"
end

datarefs_map = {
  latitude: {
    id: nil,
    name: "sim/flightmodel/position/latitude",
  },
  longitude: {
    id: nil,
    name: "sim/flightmodel/position/longitude",
  },
  mag_psi: {
    id: nil,
    name: "sim/flightmodel/position/mag_psi", # magnetic heading deg
  },
  true_psi: {
    id: nil,
    name: "sim/flightmodel/position/true_psi", # true heading deg
  },
  hpath: {
    id: nil,
    name: "sim/flightmodel/position/hpath", # track deg
  },
  elevation: {
    id: nil,
    name: "sim/flightmodel/position/elevation", # meters MSL
  },
  y_agl: {
    id: nil,
    name: "sim/flightmodel/position/y_agl", # height above ground in m
  },
  groundspeed: {
    id: nil,
    name: "sim/flightmodel/position/groundspeed", # m/s
  },
  indicated_airspeed: {
    id: nil,
    name: "sim/flightmodel/position/indicated_airspeed", # kias
  },
  indicated_airspeed2: {
    id: nil,
    name: "sim/flightmodel/position/indicated_airspeed2", # kias
  },
  true_airspeed: {
    id: nil,
    name: "sim/flightmodel/position/true_airspeed", # m/s
  },
  mach_airspeed: {
    id: nil,
    name: "sim/flightmodel/misc/machno", # m/s
  },
}

# pp api_v3_available?
datarefs = list_datarefs
# puts datarefs
# exit
bind_datarefs(datarefs, datarefs_map)

loop do
  current_data = fetch_current_data(datarefs_map)
  pp current_data
  sleep 2
end
