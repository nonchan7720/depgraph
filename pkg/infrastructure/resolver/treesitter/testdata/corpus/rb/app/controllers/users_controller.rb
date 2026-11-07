require "csv"
require_relative "../../lib/exporter"

class UsersController < ApplicationController
  def index
    @users = User.all
    Pay::Gateway.charge
  end
end
